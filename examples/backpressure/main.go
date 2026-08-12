// Command backpressure shows what happens when a subscriber cannot keep up.
//
// Queues are bounded, so a slow subscriber has to be dealt with rather than
// silently absorbed. There is no policy that is right for every stream — losing
// an event and stalling the producer are both real costs — so the hub makes the
// caller choose. This program runs the same overload against each of the four
// policies and prints exactly what each one did.
//
// The numbers below are exact, not approximate. Each run stalls the subscriber
// at a known point: the handler signals that it has started and then blocks, so
// by the time the remaining events are published the queue state is known — one
// event in the handler, queueSize in the queue, the rest hitting the policy.
//
// Run it with:
//
//	go run ./examples/backpressure
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/efureev/msghub/v3"
)

type Sample struct{ N int }

const (
	queueSize = 2  // room for two events behind the one being handled
	total     = 10 // published in each run
)

func main() {
	fmt.Printf("queue depth %d, %d events published, subscriber stalled\n",
		queueSize, total)
	fmt.Printf("→ 1 event in the handler + %d queued = %d absorbed, %d meet the policy\n\n",
		queueSize, queueSize+1, total-queueSize-1)

	run("DropNewest", msghub.DropNewest, "the event being published is discarded")
	run("DropOldest", msghub.DropOldest, "the oldest queued event is evicted to make room")
	run("Fail", msghub.Fail, "Publish reports ErrQueueFull and delivers nothing")
	runBlock()
}

// run publishes into a stalled subscriber under one policy and reports the
// outcome.
func run(name string, policy msghub.Overflow, gist string) {
	ctx := context.Background()

	h := msghub.New(
		msghub.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		msghub.WithQueueSize(queueSize),
		msghub.WithOverflow(policy),
	)
	defer func() { _ = h.Close(ctx) }()

	topic := msghub.NewTopic[Sample]("samples")

	var (
		once     sync.Once
		started  = make(chan struct{})
		release  = make(chan struct{})
		mu       sync.Mutex
		handled  []int
		refusals int
	)

	sub, err := msghub.Subscribe(h, topic, func(_ context.Context, s Sample) error {
		// Signal on the first event, then hold every invocation until the
		// publisher is done. This is what makes the counts deterministic.
		once.Do(func() { close(started) })
		<-release

		mu.Lock()
		defer mu.Unlock()
		handled = append(handled, s.N)

		return nil
	})
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	// The first event gets the worker busy.
	if err := msghub.Publish(ctx, h, topic, Sample{N: 0}); err != nil {
		panic(err)
	}
	<-started

	// Everything from here on either fits in the queue or meets the policy.
	for i := 1; i < total; i++ {
		if err := msghub.Publish(ctx, h, topic, Sample{N: i}); err != nil {
			if errors.Is(err, msghub.ErrQueueFull) {
				refusals++

				continue
			}
			panic(err)
		}
	}

	close(release)
	if err := h.Drain(ctx); err != nil {
		panic(err)
	}

	s := h.Snapshot()

	mu.Lock()
	got := append([]int(nil), handled...)
	mu.Unlock()

	fmt.Printf("== %s ==\n", name)
	fmt.Printf("  %s\n", gist)
	fmt.Printf("  delivered=%d dropped=%d\n", s.Delivered, s.Dropped)
	if refusals > 0 {
		fmt.Printf("  Publish returned ErrQueueFull %d times\n", refusals)
	}
	fmt.Printf("  handler saw %v\n\n", got)
}

// runBlock demonstrates the default separately, because it cannot be shown the
// same way: Block waits for room, so against a permanently stalled subscriber
// it would wait forever. That is exactly why Publish takes a context — the
// deadline is the way out, and the publisher learns it was throttled.
func runBlock() {
	h := msghub.New(
		msghub.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		msghub.WithQueueSize(queueSize),
	) // Block is the default
	defer func() { _ = h.Close(context.Background()) }()

	topic := msghub.NewTopic[Sample]("samples")

	var once sync.Once
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	sub, err := msghub.Subscribe(h, topic, func(hctx context.Context, _ Sample) error {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-hctx.Done(): // let Close stop us instead of hanging the program
		}

		return nil
	})
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	if err := msghub.Publish(context.Background(), h, topic, Sample{N: 0}); err != nil {
		panic(err)
	}
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var published, throttled int
	for i := 1; i < total; i++ {
		err := msghub.Publish(ctx, h, topic, Sample{N: i})
		switch {
		case err == nil:
			published++
		case errors.Is(err, context.DeadlineExceeded):
			throttled++
		default:
			panic(err)
		}
	}

	fmt.Println("== Block (default) ==")
	fmt.Println("  the publisher waits for room; nothing is lost silently")
	fmt.Printf("  %d publications fit, %d hit the publish deadline\n", published, throttled)
	fmt.Println("  without a deadline the producer is simply paced by the slowest subscriber")
}
