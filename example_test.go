package hub_test

import (
	"context"
	"errors"
	"fmt"

	hub "github.com/efureev/hubMessage/v3"
)

type OrderPlaced struct {
	ID    string
	Total int
}

// Topics are package-level values, declared once and shared by publishers and
// subscribers.
var OrdersPlaced = hub.NewTopic[OrderPlaced]("orders.placed")

func Example() {
	h := hub.New()
	defer func() { _ = h.Close(context.Background()) }()

	sub, err := hub.Subscribe(h, OrdersPlaced, func(_ context.Context, ev OrderPlaced) error {
		fmt.Printf("order %s for %d\n", ev.ID, ev.Total)

		return nil
	})
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	ctx := context.Background()
	if err := hub.Publish(ctx, h, OrdersPlaced, OrderPlaced{ID: "A-1", Total: 250}); err != nil {
		panic(err)
	}

	// Drain waits for the queued event to be handled. Without it the program
	// could exit before the subscriber goroutine ran.
	if err := h.Drain(ctx); err != nil {
		panic(err)
	}

	// Output:
	// order A-1 for 250
}

// A synchronous subscriber runs inside Publish, so its error reaches the
// publisher. Use it when the publisher cannot proceed until the handler has.
func ExampleSynchronous() {
	h := hub.New()
	defer func() { _ = h.Close(context.Background()) }()

	tooLarge := errors.New("order too large")

	sub, err := hub.Subscribe(h, OrdersPlaced, func(_ context.Context, ev OrderPlaced) error {
		if ev.Total > 100 {
			return tooLarge
		}

		return nil
	}, hub.Synchronous())
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	err = hub.Publish(context.Background(), h, OrdersPlaced, OrderPlaced{ID: "A-2", Total: 5000})
	fmt.Println(errors.Is(err, tooLarge))

	// Output:
	// true
}

// A topic name can be computed at run time, so a program can address a stream
// per tenant, per job or per shard.
func ExampleNewTopic_dynamicName() {
	h := hub.New()
	defer func() { _ = h.Close(context.Background()) }()

	ctx := context.Background()

	for _, tenant := range []string{"acme", "globex"} {
		topic := hub.NewTopic[string]("tenant." + tenant)

		sub, err := hub.Subscribe(h, topic, func(_ context.Context, msg string) error {
			fmt.Printf("%s: %s\n", tenant, msg)

			return nil
		}, hub.Synchronous())
		if err != nil {
			panic(err)
		}
		defer sub.Close()

		if err := hub.Publish(ctx, h, topic, "provisioned"); err != nil {
			panic(err)
		}
	}

	// Output:
	// acme: provisioned
	// globex: provisioned
}

// When the event type is the whole meaning of the stream, TypeTopic saves
// inventing a name to keep in sync.
func ExampleTypeTopic() {
	h := hub.New()
	defer func() { _ = h.Close(context.Background()) }()

	topic := hub.TypeTopic[OrderPlaced]()

	sub, err := hub.Subscribe(h, topic, func(_ context.Context, ev OrderPlaced) error {
		fmt.Println("received", ev.ID)

		return nil
	}, hub.Synchronous())
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	if err := hub.Publish(context.Background(), h, topic, OrderPlaced{ID: "A-3"}); err != nil {
		panic(err)
	}

	// Output:
	// received A-3
}

// A subscriber that cannot keep up is a decision, not an accident: pick what
// should give way when its queue fills.
func ExampleWithOverflow() {
	h := hub.New(
		hub.WithQueueSize(1),
		hub.WithOverflow(hub.Fail),
	)
	defer func() { _ = h.Close(context.Background()) }()

	topic := hub.NewTopic[int]("samples")
	block := make(chan struct{})

	sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
		<-block

		return nil
	})
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	ctx := context.Background()
	var refused int
	for i := range 10 {
		if err := hub.Publish(ctx, h, topic, i); errors.Is(err, hub.ErrQueueFull) {
			refused++
		}
	}
	close(block)

	fmt.Println("refused:", refused > 0)

	// Output:
	// refused: true
}

// A handler that fails is reported rather than swallowed.
func ExampleWithErrorHandler() {
	h := hub.New(hub.WithErrorHandler(func(_ context.Context, topic string, err error) {
		fmt.Printf("%s: %v\n", topic, err)
	}))
	defer func() { _ = h.Close(context.Background()) }()

	topic := hub.NewTopic[int]("jobs")

	sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
		return errors.New("disk full")
	}, hub.Synchronous())
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	_ = hub.Publish(context.Background(), h, topic, 1)

	// Output:
	// jobs[int]: disk full
}
