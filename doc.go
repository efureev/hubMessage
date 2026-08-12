// Package msghub is a typed, asynchronous, in-process publish/subscribe bus.
//
// Events travel on named, typed topics. A [Topic] is an ordinary value built
// from a plain string and a type parameter:
//
//	var UserCreated = msghub.NewTopic[User]("user.created")
//
// Both halves of the key matter. The name lets a program address a stream it
// computes at run time; the type makes the payload checked at compile time, so
// a handler can never be invoked with arguments it does not accept.
//
// # Delivery
//
// A subscriber owns a buffered queue and a goroutine draining it, so [Publish]
// hands the event over and returns instead of running the handler. Events reach
// one subscriber in publication order; the relative order across subscribers is
// unspecified.
//
// A subscriber registered with [Synchronous] runs inline in [Publish] instead,
// and its error is returned to the publisher. Use it when a handler must
// complete — or fail — before the publisher proceeds.
//
// # Backpressure
//
// Queues are bounded, so a subscriber that cannot keep up must be dealt with
// rather than silently absorbed. What happens when its queue is full is chosen
// by the caller, per hub or per subscription: block until there is room (the
// default), drop the new event, evict the oldest, or fail the publish. See
// [Overflow].
//
// # Failures are reported
//
// A handler that returns an error or panics is not silently ignored: the
// failure reaches [WithErrorHandler] and [WithLogger], and is counted in
// [Hub.Snapshot]. A panic is recovered and converted to an error, so one
// misbehaving subscriber cannot take down the publisher.
//
// # Shutdown
//
// [Hub.Drain] waits for queued events to be handled; [Hub.Close] stops every
// subscriber goroutine and rejects further use. Both take a context, so a stuck
// handler surfaces as a deadline rather than a hang.
//
// # Package layout
//
//	doc.go          — this overview and the file map.
//	topic.go        — Topic[T], NewTopic, TypeTopic and the internal topic key.
//	hub.go          — Hub, New, Topics, Drain, Close and in-flight accounting.
//	subscription.go — Subscription, Subscribe and the per-subscriber worker.
//	publish.go      — Publish, the Overflow policies and handler invocation.
//	options.go      — Option, SubOption and the With* constructors.
//	errors.go       — the sentinel errors.
//	stats.go        — the delivery counters behind Hub.Snapshot.
//
// Runnable demos live in examples/, one main package per directory: basic,
// delivery, backpressure and failures. They show the bus behaving over time —
// queues filling, events dropped, counters moving — which the Example
// functions here cannot.
//
// The bus knows nothing about application lifecycles. To tie a hub and its
// subscriptions to github.com/efureev/appmod modules, use the adapter module
// github.com/efureev/appmod/adapters/hubmod.
package msghub
