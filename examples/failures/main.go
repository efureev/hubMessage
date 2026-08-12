// Command failures shows that nothing goes missing when a handler misbehaves.
//
// A handler that returns an error, and one that panics, are both reported: to
// the error handler, to the structured logger, and in the counters. A panic is
// recovered into a *msghub.PanicError carrying the value and the stack captured
// at recovery — the only record of where it came from, since the goroutine that
// produced it does not survive.
//
// This is deliberate. A bus that swallows failures makes a subscriber that
// silently stopped working indistinguishable from one that has nothing to do.
//
// Run it with:
//
//	go run ./examples/failures
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/efureev/msghub/v3"
)

type Payment struct{ ID string }

var errDeclined = errors.New("card declined")

func main() {
	ctx := context.Background()

	// Everything the hub logs lands in this buffer so the demo can show it.
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{
		Level: slog.LevelError,
		// Drop the timestamp: it would make the output differ between runs.
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return a
		},
	}))

	var (
		mu       sync.Mutex
		observed []error
	)

	h := msghub.New(
		msghub.WithLogger(logger),
		msghub.WithErrorHandler(func(_ context.Context, topic string, err error) {
			mu.Lock()
			defer mu.Unlock()
			observed = append(observed, fmt.Errorf("%s: %w", topic, err))
		}),
	)
	defer func() { _ = h.Close(ctx) }()

	topic := msghub.NewTopic[Payment]("payments")

	returnsError, err := msghub.Subscribe(h, topic, func(context.Context, Payment) error {
		return errDeclined
	})
	if err != nil {
		panic(err)
	}
	defer returnsError.Close()

	panics, err := msghub.Subscribe(h, topic, func(_ context.Context, p Payment) error {
		panic("no route for " + p.ID)
	})
	if err != nil {
		panic(err)
	}
	defer panics.Close()

	healthy, err := msghub.Subscribe(h, topic, func(context.Context, Payment) error {
		return nil
	})
	if err != nil {
		panic(err)
	}
	defer healthy.Close()

	// Publish returns nil: these are asynchronous subscribers, so their
	// failures have nowhere to travel back to.
	if err := msghub.Publish(ctx, h, topic, Payment{ID: "pay_1"}); err != nil {
		panic(err)
	}
	if err := h.Drain(ctx); err != nil {
		panic(err)
	}

	fmt.Println("== Publish returned ==")
	fmt.Println("  <nil> — an asynchronous handler has no publisher left to report to")
	fmt.Println()

	reportObserved(&mu, &observed)
	reportPanicDetail(&mu, &observed)
	reportCounters(h)
	reportLogs(&logs)
	reportSynchronous(ctx)
}

// reportObserved prints what the error handler received, sorted so the output
// does not depend on which subscriber goroutine ran first.
func reportObserved(mu *sync.Mutex, observed *[]error) {
	mu.Lock()
	defer mu.Unlock()

	lines := make([]string, 0, len(*observed))
	for _, e := range *observed {
		lines = append(lines, e.Error())
	}
	sort.Strings(lines)

	fmt.Println("== error handler ==")
	for _, l := range lines {
		fmt.Println(" ", l)
	}
	fmt.Println()
}

// reportPanicDetail digs the typed error out of what was observed. A panic is
// not flattened into a string: errors.As reaches the value and the stack.
func reportPanicDetail(mu *sync.Mutex, observed *[]error) {
	mu.Lock()
	defer mu.Unlock()

	fmt.Println("== the panic, in detail ==")

	for _, e := range *observed {
		var pe *msghub.PanicError
		if !errors.As(e, &pe) {
			continue
		}

		fmt.Printf("  topic: %s\n", pe.Topic)
		fmt.Printf("  value: %#v\n", pe.Value)
		fmt.Printf("  stack: captured, deepest application frame is %s\n", topFrame(pe.Stack))
	}

	fmt.Println()
}

// reportCounters shows the same events as numbers, which is what a metrics
// exporter would read.
func reportCounters(h *msghub.Hub) {
	s := h.Snapshot()

	fmt.Println("== counters ==")
	fmt.Printf("  published=%d delivered=%d failed=%d panicked=%d dropped=%d\n",
		s.Published, s.Delivered, s.Failed, s.Panicked, s.Dropped)
	fmt.Println("  one publication, three subscribers: one succeeded, two failed")
	fmt.Println("  every panic is also counted as a failure")
	fmt.Println()
}

// reportLogs shows that the failures were logged as well, with the topic
// attached.
func reportLogs(logs *bytes.Buffer) {
	fmt.Println("== structured log ==")

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Println(" ", l)
	}
	fmt.Println()
}

// reportSynchronous closes the loop: a synchronous subscriber's panic reaches
// the publisher as an error instead of unwinding the stack into it.
func reportSynchronous(ctx context.Context) {
	h := msghub.New(msghub.WithLogger(nil))
	defer func() { _ = h.Close(ctx) }()

	topic := msghub.NewTopic[Payment]("checkout")

	sub, err := msghub.Subscribe(h, topic, func(context.Context, Payment) error {
		panic(errDeclined) // panicking with an error value
	}, msghub.Synchronous())
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	err = msghub.Publish(ctx, h, topic, Payment{ID: "pay_2"})

	fmt.Println("== a synchronous panic ==")
	fmt.Printf("  Publish returned an error instead of crashing: %v\n", err != nil)
	fmt.Printf("  errors.Is(err, errDeclined) → %t\n", errors.Is(err, errDeclined))
	fmt.Println("  the panic value survives the trip, so sentinels still work")
}

// topFrame extracts the first application function name from a stack trace,
// which is enough to show the stack is real without printing a page of it.
//
// The arguments are cut off on purpose: they contain pointer values, and this
// program's output has to be identical on every run.
func topFrame(stack []byte) string {
	for _, l := range strings.Split(string(stack), "\n") {
		if !strings.HasPrefix(l, "main.") {
			continue
		}
		if i := strings.IndexByte(l, '('); i > 0 {
			return l[:i]
		}

		return strings.TrimSpace(l)
	}

	return "unavailable"
}
