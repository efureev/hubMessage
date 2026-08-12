package msghub_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/efureev/msghub/v3"
)

type benchEvent struct {
	ID string
	N  int
}

var benchTopic = msghub.NewTopic[benchEvent]("bench")

func benchHub(tb testing.TB, opts ...msghub.Option) *msghub.Hub {
	tb.Helper()

	base := []msghub.Option{msghub.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	h := msghub.New(append(base, opts...)...)
	tb.Cleanup(func() { _ = h.Close(context.Background()) })

	return h
}

// The asynchronous path: hand the event to a queue and return. The queue is
// deep enough that the benchmark measures the handoff rather than backpressure.
func BenchmarkPublishAsync(b *testing.B) {
	h := benchHub(b, msghub.WithQueueSize(1024))
	ctx := context.Background()

	sub, err := msghub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil })
	if err != nil {
		b.Fatal(err)
	}
	defer sub.Close()

	ev := benchEvent{ID: "x", N: 1}

	b.ReportAllocs()
	for b.Loop() {
		if err := msghub.Publish(ctx, h, benchTopic, ev); err != nil {
			b.Fatal(err)
		}
	}

	_ = h.Drain(ctx)
}

// The synchronous path: no queue, no goroutine, the handler runs inline.
func BenchmarkPublishSync(b *testing.B) {
	h := benchHub(b)
	ctx := context.Background()

	sub, err := msghub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil },
		msghub.Synchronous())
	if err != nil {
		b.Fatal(err)
	}
	defer sub.Close()

	ev := benchEvent{ID: "x", N: 1}

	b.ReportAllocs()
	for b.Loop() {
		if err := msghub.Publish(ctx, h, benchTopic, ev); err != nil {
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
		sub, err := msghub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil },
			msghub.Synchronous())
		if err != nil {
			b.Fatal(err)
		}
		defer sub.Close()
	}

	ev := benchEvent{ID: "x", N: 1}

	b.ReportAllocs()
	for b.Loop() {
		if err := msghub.Publish(ctx, h, benchTopic, ev); err != nil {
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
		if err := msghub.Publish(ctx, h, benchTopic, ev); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPublishParallel(b *testing.B) {
	h := benchHub(b, msghub.WithQueueSize(4096))
	ctx := context.Background()

	sub, err := msghub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil })
	if err != nil {
		b.Fatal(err)
	}
	defer sub.Close()

	ev := benchEvent{ID: "x", N: 1}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = msghub.Publish(ctx, h, benchTopic, ev)
		}
	})

	_ = h.Drain(ctx)
}

func BenchmarkSubscribeClose(b *testing.B) {
	h := benchHub(b)

	b.ReportAllocs()
	for b.Loop() {
		sub, err := msghub.Subscribe(h, benchTopic, func(context.Context, benchEvent) error { return nil },
			msghub.Synchronous())
		if err != nil {
			b.Fatal(err)
		}
		sub.Close()
	}
}
