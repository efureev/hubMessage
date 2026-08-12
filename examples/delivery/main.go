// Command delivery puts the two delivery modes side by side.
//
// A subscriber is asynchronous unless told otherwise: it owns a bounded queue
// and a goroutine, Publish hands the event over and returns, and a handler
// error never travels back to the publisher. Synchronous() runs the handler
// inline instead, so Publish returns only after it has finished — and returns
// its error.
//
// Both kinds can listen on the same topic, which the last section shows.
//
// Run it with:
//
//	go run ./examples/delivery
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"

	"github.com/efureev/msghub/v3"
)

type Job struct{ N int }

// errRejected is what the synchronous validator returns.
var errRejected = errors.New("job rejected")

func main() {
	ctx := context.Background()

	// A quiet logger keeps the demo output to what the program prints itself;
	// handler failures still reach the error handler and the counters.
	h := msghub.New(msghub.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer func() { _ = h.Close(ctx) }()

	whenTheHandlerRuns(ctx, h)
	whereTheErrorGoes(ctx, h)
	ordering(ctx, h)
	mixed(ctx, h)
}

// whenTheHandlerRuns shows the timing difference. The synchronous handler has
// finished by the time Publish returns; the asynchronous one has not, which is
// why Drain exists.
func whenTheHandlerRuns(ctx context.Context, h *msghub.Hub) {
	fmt.Println("== when the handler runs ==")

	syncTopic := msghub.NewTopic[Job]("jobs.sync")
	asyncTopic := msghub.NewTopic[Job]("jobs.async")

	done := make(chan struct{})

	s1, err := msghub.Subscribe(h, syncTopic, func(context.Context, Job) error {
		fmt.Println("  [sync]  handler ran")

		return nil
	}, msghub.Synchronous())
	if err != nil {
		panic(err)
	}
	defer s1.Close()

	s2, err := msghub.Subscribe(h, asyncTopic, func(context.Context, Job) error {
		fmt.Println("  [async] handler ran")
		close(done)

		return nil
	})
	if err != nil {
		panic(err)
	}
	defer s2.Close()

	if err := msghub.Publish(ctx, h, syncTopic, Job{N: 1}); err != nil {
		panic(err)
	}
	fmt.Println("  [sync]  Publish returned")

	if err := msghub.Publish(ctx, h, asyncTopic, Job{N: 2}); err != nil {
		panic(err)
	}
	fmt.Println("  [async] Publish returned")

	<-done
	fmt.Println()
}

// whereTheErrorGoes shows the other half of the difference. A synchronous
// handler reports to the publisher; an asynchronous one has no publisher left
// to report to by the time it runs, so its error goes to the error handler, the
// logger and the counters.
func whereTheErrorGoes(ctx context.Context, h *msghub.Hub) {
	fmt.Println("== where the error goes ==")

	var (
		mu       sync.Mutex
		reported []string
	)
	hub := msghub.New(
		msghub.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		msghub.WithErrorHandler(func(_ context.Context, topic string, err error) {
			mu.Lock()
			defer mu.Unlock()
			reported = append(reported, fmt.Sprintf("%s: %v", topic, err))
		}),
	)
	defer func() { _ = hub.Close(ctx) }()

	syncTopic := msghub.NewTopic[Job]("validate")
	asyncTopic := msghub.NewTopic[Job]("index")

	s1, err := msghub.Subscribe(hub, syncTopic, func(context.Context, Job) error {
		return errRejected
	}, msghub.Synchronous())
	if err != nil {
		panic(err)
	}
	defer s1.Close()

	s2, err := msghub.Subscribe(hub, asyncTopic, func(context.Context, Job) error {
		return errRejected
	})
	if err != nil {
		panic(err)
	}
	defer s2.Close()

	err = msghub.Publish(ctx, hub, syncTopic, Job{N: 1})
	fmt.Printf("  [sync]  Publish returned %v (errors.Is → %t)\n", err, errors.Is(err, errRejected))

	err = msghub.Publish(ctx, hub, asyncTopic, Job{N: 2})
	fmt.Printf("  [async] Publish returned %v\n", err)

	if err := hub.Drain(ctx); err != nil {
		panic(err)
	}

	// Both failures reach the error handler. Returning a synchronous error to
	// the publisher does not exempt it from reporting: observability must not
	// undercount just because someone was listening.
	mu.Lock()
	sort.Strings(reported)
	for _, line := range reported {
		fmt.Println("  error handler saw:", line)
	}
	mu.Unlock()

	fmt.Printf("  counters: failed=%d\n\n", hub.Snapshot().Failed)
}

// ordering shows the guarantee an asynchronous subscriber does give: events
// reach it in publication order, because one goroutine drains one queue.
func ordering(ctx context.Context, h *msghub.Hub) {
	fmt.Println("== per-subscriber ordering ==")

	topic := msghub.NewTopic[Job]("stream")

	var (
		mu   sync.Mutex
		got  []int
		want = 8
	)

	sub, err := msghub.Subscribe(h, topic, func(_ context.Context, j Job) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, j.N)

		return nil
	}, msghub.WithSubQueueSize(want))
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	for i := range want {
		if err := msghub.Publish(ctx, h, topic, Job{N: i}); err != nil {
			panic(err)
		}
	}
	if err := h.Drain(ctx); err != nil {
		panic(err)
	}

	mu.Lock()
	fmt.Printf("  received %v\n", got)
	mu.Unlock()

	fmt.Println("  order is guaranteed per subscriber, not across subscribers")
	fmt.Println()
}

// mixed shows both kinds on one topic. The synchronous validator can veto the
// publication while the asynchronous indexer does its work off the hot path.
func mixed(ctx context.Context, h *msghub.Hub) {
	fmt.Println("== both kinds on one topic ==")

	topic := msghub.NewTopic[Job]("submissions")
	indexed := make(chan int, 4)

	validator, err := msghub.Subscribe(h, topic, func(_ context.Context, j Job) error {
		if j.N%2 == 1 {
			return errRejected
		}

		return nil
	}, msghub.Synchronous())
	if err != nil {
		panic(err)
	}
	defer validator.Close()

	indexer, err := msghub.Subscribe(h, topic, func(_ context.Context, j Job) error {
		indexed <- j.N

		return nil
	})
	if err != nil {
		panic(err)
	}
	defer indexer.Close()

	for i := range 4 {
		err := msghub.Publish(ctx, h, topic, Job{N: i})
		fmt.Printf("  job %d: publish err = %v\n", i, err)
	}

	if err := h.Drain(ctx); err != nil {
		panic(err)
	}
	close(indexed)

	// Every job reached the indexer: a synchronous handler reporting an error
	// does not stop delivery to the others.
	var seen []int
	for n := range indexed {
		seen = append(seen, n)
	}
	fmt.Printf("  indexer still saw %v\n", seen)
}
