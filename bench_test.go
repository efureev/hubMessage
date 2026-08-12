package hub_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	hub "github.com/efureev/hubMessage/v3"
)

type benchEvent struct {
	ID string
	N  int
}

var benchTopic = hub.NewTopic[benchEvent]("bench")

func benchHub(tb testing.TB, opts ...hub.Option) *hub.Hub {
	tb.Helper()

	base := []hub.Option{hub.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	h := hub.New(append(base, opts...)...)
	tb.Cleanup(func() { _ = h.Close(context.Background()) })

	return h
}

// The asynchronous path: hand the event to a queue and return. The queue is
// deep enough that the benchmark measures the handoff rather than backpressure.
func BenchmarkPublishAsync(b *testing.B) {
	h := benchHub(b, hub.WithQueueSize(1024))
	ctx := context.Background()

	sub, err := hub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil })
	if err != nil {
		b.Fatal(err)
	}
	defer sub.Close()

	ev := benchEvent{ID: "x", N: 1}

	b.ReportAllocs()
	for b.Loop() {
		if err := hub.Publish(ctx, h, benchTopic, ev); err != nil {
			b.Fatal(err)
		}
	}

	_ = h.Drain(ctx)
}

// The synchronous path: no queue, no goroutine, the handler runs inline.
func BenchmarkPublishSync(b *testing.B) {
	h := benchHub(b)
	ctx := context.Background()

	sub, err := hub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil },
		hub.Synchronous())
	if err != nil {
		b.Fatal(err)
	}
	defer sub.Close()

	ev := benchEvent{ID: "x", N: 1}

	b.ReportAllocs()
	for b.Loop() {
		if err := hub.Publish(ctx, h, benchTopic, ev); err != nil {
			b.Fatal(err)
		}
	}
}

// Fan-out to several synchronous subscribers, which isolates the per-subscriber
// cost from the queueing machinery.
func BenchmarkPublishSyncFanOut(b *testing.B) {
	h := benchHub(b)
	ctx := context.Background()

	for range 8 {
		sub, err := hub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil },
			hub.Synchronous())
		if err != nil {
			b.Fatal(err)
		}
		defer sub.Close()
	}

	ev := benchEvent{ID: "x", N: 1}

	b.ReportAllocs()
	for b.Loop() {
		if err := hub.Publish(ctx, h, benchTopic, ev); err != nil {
			b.Fatal(err)
		}
	}
}

// Publishing where nobody listens must cost close to nothing: a producer should
// not have to know whether anything is subscribed.
func BenchmarkPublishNoSubscribers(b *testing.B) {
	h := benchHub(b)
	ctx := context.Background()
	ev := benchEvent{ID: "x", N: 1}

	b.ReportAllocs()
	for b.Loop() {
		if err := hub.Publish(ctx, h, benchTopic, ev); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPublishParallel(b *testing.B) {
	h := benchHub(b, hub.WithQueueSize(4096))
	ctx := context.Background()

	sub, err := hub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil })
	if err != nil {
		b.Fatal(err)
	}
	defer sub.Close()

	ev := benchEvent{ID: "x", N: 1}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = hub.Publish(ctx, h, benchTopic, ev)
		}
	})

	_ = h.Drain(ctx)
}

func BenchmarkSubscribeClose(b *testing.B) {
	h := benchHub(b)

	b.ReportAllocs()
	for b.Loop() {
		sub, err := hub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil },
			hub.Synchronous())
		if err != nil {
			b.Fatal(err)
		}
		sub.Close()
	}
}
