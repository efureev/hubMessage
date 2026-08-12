// Command basic is the five-minute tour: build a topic, subscribe to it,
// publish, and shut the hub down cleanly.
//
// It also shows the two ways to build a topic. NewTopic takes a name computed
// at run time — per tenant, per shard, per job — while TypeTopic keys the
// stream by its payload type alone, for events whose type is already the whole
// meaning.
//
// Run it with:
//
//	go run ./examples/basic
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/efureev/msghub/v3"
)

// OrderPlaced is a domain event. Topics are typed, so a handler can never be
// invoked with anything else.
type OrderPlaced struct {
	ID    string
	Total int
}

// ServiceStarted is keyed by its type alone: naming it would only add a string
// to keep in sync.
type ServiceStarted struct{ Name string }

func main() {
	ctx := context.Background()

	h := msghub.New()
	defer func() { _ = h.Close(ctx) }()

	fanOut(ctx, h)
	perTenant(ctx, h)
	typeKeyed(ctx, h)

	// Drain waits for every accepted event to be handled. Without it the
	// program could exit while the subscriber goroutines still had work.
	if err := h.Drain(ctx); err != nil {
		panic(err)
	}

	report(h)
}

// fanOut shows one publication reaching several subscribers. Each owns a queue
// and a goroutine, so the results are collected under a mutex and printed once
// everything has drained — the order in which subscribers run is unspecified.
func fanOut(ctx context.Context, h *msghub.Hub) {
	fmt.Println("== fan-out ==")

	orders := msghub.NewTopic[OrderPlaced]("orders.placed")

	var (
		mu   sync.Mutex
		seen []string
	)
	record := func(who string, ev OrderPlaced) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, fmt.Sprintf("%s: order %s for %d", who, ev.ID, ev.Total))
	}

	billing, err := msghub.Subscribe(h, orders, func(_ context.Context, ev OrderPlaced) error {
		record("billing", ev)

		return nil
	})
	if err != nil {
		panic(err)
	}
	defer billing.Close()

	audit, err := msghub.Subscribe(h, orders, func(_ context.Context, ev OrderPlaced) error {
		record("audit", ev)

		return nil
	})
	if err != nil {
		panic(err)
	}
	defer audit.Close()

	if err := msghub.Publish(ctx, h, orders, OrderPlaced{ID: "A-1", Total: 250}); err != nil {
		panic(err)
	}
	if err := h.Drain(ctx); err != nil {
		panic(err)
	}

	mu.Lock()
	sort.Strings(seen)
	for _, line := range seen {
		fmt.Println(" ", line)
	}
	mu.Unlock()

	fmt.Println("  one publication, two independent subscribers")
	fmt.Println()
}

// perTenant builds topic names at run time. This is the everyday case a bus
// keyed only by Go type cannot express: three streams of the same payload type,
// addressed separately.
func perTenant(ctx context.Context, h *msghub.Hub) {
	fmt.Println("== run-time topic names ==")

	for _, tenant := range []string{"acme", "globex"} {
		name := "tenant." + tenant + ".orders"
		topic := msghub.NewTopic[OrderPlaced](name)

		// Synchronous keeps the output in publication order here; the
		// asynchronous default is shown in examples/delivery.
		sub, err := msghub.Subscribe(h, topic, func(_ context.Context, ev OrderPlaced) error {
			fmt.Printf("  %s -> order %s\n", tenant, ev.ID)

			return nil
		}, msghub.Synchronous())
		if err != nil {
			panic(err)
		}
		defer sub.Close()

		if err := msghub.Publish(ctx, h, topic, OrderPlaced{ID: strings.ToUpper(tenant) + "-1"}); err != nil {
			panic(err)
		}
	}

	fmt.Println()
}

// typeKeyed shows a topic identified by its payload type. Note that a name and
// a type together form the key: NewTopic[T]("x") and NewTopic[U]("x") are two
// independent streams, not a collision.
func typeKeyed(ctx context.Context, h *msghub.Hub) {
	fmt.Println("== type-keyed topic ==")

	started := msghub.TypeTopic[ServiceStarted]()

	sub, err := msghub.Subscribe(h, started, func(_ context.Context, ev ServiceStarted) error {
		fmt.Printf("  %s started\n", ev.Name)

		return nil
	}, msghub.Synchronous())
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	if err := msghub.Publish(ctx, h, started, ServiceStarted{Name: "scheduler"}); err != nil {
		panic(err)
	}

	fmt.Printf("  topic name is %q, identity is %q\n", started.Name(), started.String())
	fmt.Println()
}

// report prints what the hub knows about itself. Topics lists the streams that
// currently have subscribers, and Snapshot the delivery counters.
func report(h *msghub.Hub) {
	fmt.Println("== hub state ==")

	for _, t := range h.Topics() {
		fmt.Println("  topic:", t)
	}

	// Delivered exceeds Published because the counters measure different
	// things: one publication to a topic with two subscribers is one Published
	// and two Delivered.
	s := h.Snapshot()
	fmt.Printf("  published=%d delivered=%d dropped=%d failed=%d\n",
		s.Published, s.Delivered, s.Dropped, s.Failed)
}
