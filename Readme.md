# hubMessage

[![Test](https://github.com/efureev/hubMessage/actions/workflows/test.yml/badge.svg)](https://github.com/efureev/hubMessage/actions/workflows/test.yml)
[![Codacy Badge](https://api.codacy.com/project/badge/Grade/0cdced379f3e41d39732a720263c8393)](https://app.codacy.com/app/efureev/hubMessage?utm_source=github.com&utm_medium=referral&utm_content=efureev/hubMessage&utm_campaign=Badge_Grade_Dashboard)
[![Maintainability](https://api.codeclimate.com/v1/badges/82d6074b251f785f8c23/maintainability)](https://codeclimate.com/github/efureev/hubMessage/maintainability)
[![Test Coverage](https://api.codeclimate.com/v1/badges/82d6074b251f785f8c23/test_coverage)](https://codeclimate.com/github/efureev/hubMessage/test_coverage)
[![codecov](https://codecov.io/gh/efureev/hubMessage/branch/master/graph/badge.svg)](https://codecov.io/gh/efureev/hubMessage)
[![Go Report Card](https://goreportcard.com/badge/github.com/efureev/hubMessage)](https://goreportcard.com/report/github.com/efureev/hubMessage)

A typed, asynchronous, in-process event bus for Go: named topics, per-subscriber FIFO queues,
explicit backpressure, zero dependencies.

Events travel on topics that are ordinary values, carrying both a name and a payload type:

```go
var UserCreated = hub.NewTopic[User]("user.created")
```

The name lets a program address a stream it computes at run time — per tenant, per shard, per
job. The type makes the payload checked at compile time, so a handler can never be invoked
with arguments it does not accept.

### Features

- **Typed topics.** No `interface{}`, no reflection on the hot path, no signature mismatches.
- **Asynchronous by default.** Each subscriber owns a bounded queue and a goroutine, so a slow
  handler delays only itself. Events reach one subscriber in publication order.
- **Synchronous when it matters.** `Synchronous()` runs the handler inline and returns its
  error to the publisher.
- **Explicit backpressure.** A full queue blocks, drops the newest, evicts the oldest or
  fails — your choice, per hub or per subscription.
- **Failures are visible.** A handler that returns an error or panics is reported to an error
  handler, logged, and counted. Nothing is swallowed.
- **Context everywhere.** `Publish`, `Drain` and `Close` all take one, so a stuck handler
  surfaces as a deadline rather than a hang.
- **Handle-based unsubscribe.** `Subscribe` returns a handle. Two subscriptions of the same
  function — or of a method value from two different receivers — are independent.

### Requirements

- Go 1.25+

### Install

```bash
go get -u github.com/efureev/hubMessage/v3
```

> The module path is `github.com/efureev/hubMessage/v3`, the package name is `hub`.

## Quick start

```go
package main

import (
	"context"
	"fmt"

	hub "github.com/efureev/hubMessage/v3"
)

type OrderPlaced struct {
	ID    string
	Total int
}

var OrdersPlaced = hub.NewTopic[OrderPlaced]("orders.placed")

func main() {
	ctx := context.Background()

	h := hub.New()
	defer func() { _ = h.Close(ctx) }()

	sub, err := hub.Subscribe(h, OrdersPlaced, func(_ context.Context, ev OrderPlaced) error {
		fmt.Printf("order %s for %d\n", ev.ID, ev.Total)

		return nil
	})
	if err != nil {
		panic(err)
	}
	defer sub.Close()

	if err := hub.Publish(ctx, h, OrdersPlaced, OrderPlaced{ID: "A-1", Total: 250}); err != nil {
		panic(err)
	}

	// Wait for the queued event to be handled before the program exits.
	if err := h.Drain(ctx); err != nil {
		panic(err)
	}
}
```

`Publish` and `Subscribe` are package functions rather than methods because Go has no generic
methods: a type parameter cannot appear on a method. The hub is the first argument instead.

## API overview

| Function / Method                                             | Description                                                             |
|---------------------------------------------------------------|-------------------------------------------------------------------------|
| `hub.NewTopic[T](name string) Topic[T]`                       | Build a topic keyed by name **and** payload type.                        |
| `hub.TypeTopic[T]() Topic[T]`                                 | Build a topic keyed by the payload type alone.                           |
| `(t Topic[T]) Name() string`                                  | The topic name; empty for a `TypeTopic`.                                 |
| `(t Topic[T]) String() string`                                | `name[type]`, or the bare type when unnamed.                             |
| `hub.New(opts ...Option) *Hub`                                | Create a hub.                                                            |
| `hub.Subscribe[T](h, t, fn, opts...) (Subscription, error)`   | Register a handler and get a handle that removes it.                     |
| `hub.Publish[T](ctx, h, t, ev) error`                         | Deliver `ev` to every subscriber of the topic.                           |
| `(h *Hub) Drain(ctx) error`                                   | Block until every accepted event has been handled.                       |
| `(h *Hub) Close(ctx) error`                                   | Stop every subscriber goroutine and reject further use. Idempotent.      |
| `(h *Hub) Topics() []string`                                  | Sorted identifiers of topics that currently have subscribers.            |
| `(h *Hub) Snapshot() Stats`                                   | Published / delivered / dropped / panicked / failed counters.            |
| `(s Subscription) Close()`                                    | Remove the handler. Idempotent, safe from inside the handler.            |
| `(s Subscription) Topic() string`                             | The topic this subscription listens on.                                  |

### Hub options

| Option                              | Description                                                          |
|-------------------------------------|----------------------------------------------------------------------|
| `WithQueueSize(n int)`              | Default per-subscriber queue depth. Default `64`; `0` is a rendezvous.|
| `WithOverflow(p Overflow)`          | Default policy for a full queue. Default `Block`.                     |
| `WithLogger(l *slog.Logger)`        | Logger for handler failures. Default `slog.Default()`; `nil` disables.|
| `WithErrorHandler(fn)`              | Callback for every handler error and recovered panic.                 |

### Subscription options

| Option                        | Description                                                        |
|-------------------------------|--------------------------------------------------------------------|
| `WithSubQueueSize(n int)`     | Override the queue depth for this subscription.                    |
| `WithSubOverflow(p Overflow)` | Override the full-queue policy for this subscription.              |
| `Synchronous()`               | Run the handler inline in `Publish` and return its error.          |

## Delivery modes

A subscriber is asynchronous unless you say otherwise.

**Asynchronous** — the handler runs on its own goroutine, fed by a bounded queue. `Publish`
hands the event over and returns. Errors do not travel back to the publisher; they go to the
error handler, the logger and the counters. Events reach *this* subscriber in publication
order; the relative order across subscribers is unspecified.

**Synchronous** (`Synchronous()`) — the handler runs inside `Publish`, and its error is joined
into `Publish`'s return value. Use it when the publisher cannot proceed until the handler has:
a validation step, a write the next statement depends on. The cost is the publisher's time,
and `WithQueueSize`/`Overflow` no longer apply — there is no queue to fill.

The two mix freely on one topic.

## Backpressure

Queues are bounded, so a subscriber that cannot keep up has to be dealt with rather than
silently absorbed. There is no policy that is right for every stream, so the hub asks:

| `Overflow`   | When the queue is full                        | Use for                                        |
|--------------|-----------------------------------------------|------------------------------------------------|
| `Block`      | Wait for room, or for the publish context.    | Default. Nothing may be lost.                  |
| `DropNewest` | Discard the event being published.            | A gap beats a delay; no sample is special.     |
| `DropOldest` | Evict the oldest queued event.                | Only recent events are useful: metrics, state. |
| `Fail`       | Return `ErrQueueFull`, deliver nothing.       | The caller decides what to do.                 |

```go
h := hub.New(hub.WithQueueSize(1024), hub.WithOverflow(hub.DropOldest))

// ...but this one must not lose anything, however slow it gets.
sub, err := hub.Subscribe(h, Audit, writeAuditLog,
	hub.WithSubOverflow(hub.Block), hub.WithSubQueueSize(64))
```

## Failure handling

A handler that returns an error or panics is never silently dropped:

```go
h := hub.New(hub.WithErrorHandler(func(ctx context.Context, topic string, err error) {
	metrics.HandlerFailures.WithLabelValues(topic).Inc()

	var pe *hub.PanicError
	if errors.As(err, &pe) {
		log.Printf("%s panicked with %#v\n%s", pe.Topic, pe.Value, pe.Stack)
	}
}))
```

A panic is recovered and converted into a `*hub.PanicError` carrying the recovered value and
the stack captured at the point of recovery — the only record of where it came from, since the
goroutine that produced it does not survive. If the panic value is itself an `error`,
`errors.Is` and `errors.As` reach through.

`Hub.Snapshot()` reports `Published`, `Delivered`, `Dropped`, `Panicked` and `Failed`.

## Shutdown

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := h.Drain(ctx); err != nil {   // let queued events finish
	log.Printf("drain: %v", err)
}
if err := h.Close(ctx); err != nil {   // stop the goroutines
	log.Printf("close: %v", err)
}
```

`Drain` reports a moment at which nothing was outstanding, not a promise that nothing will be
published afterwards — drain once the publishers have stopped. `Close` abandons whatever is
still queued, so drain first when those events matter.

## Application lifecycle

The bus knows nothing about application lifecycles, and does not depend on
[`appmod`](https://github.com/efureev/appmod). To tie a hub and its subscriptions to appmod
modules — a module owning the hub, subscriptions removed automatically on `Destroy` — use the
adapter module `github.com/efureev/appmod/adapters/hubmod`.

## Package layout

The package is flat; every file sits in the repository root.

| File               | Responsibility                                                  |
|--------------------|------------------------------------------------------------------|
| `doc.go`           | Package overview and this file map.                              |
| `topic.go`         | `Topic[T]`, `NewTopic`, `TypeTopic` and the internal topic key.  |
| `hub.go`           | `Hub`, `New`, `Topics`, `Drain`, `Close`, in-flight accounting.  |
| `subscription.go`  | `Subscription`, `Subscribe` and the per-subscriber worker.       |
| `publish.go`       | `Publish`, the `Overflow` policies and handler invocation.       |
| `options.go`       | `Option`, `SubOption` and the `With*` constructors.              |
| `errors.go`        | The sentinel errors and `PanicError`.                            |
| `stats.go`         | The delivery counters behind `Hub.Snapshot`.                     |

## Development

The project ships with a containerized dev setup (see `docker-compose.yml`), so you don't
need a local Go toolchain or `golangci-lint` installed. All commands are wrapped in the
`Makefile`:

```bash
make            # show available commands
make test       # run linter + tests (race detector + coverage) in containers
make gotest     # run tests only
make lint       # run golangci-lint only
make fmt        # gofmt + goimports + go mod tidy
make cover      # generate coverage.html
make shell      # open a shell inside the Go container
make clean      # tear down containers and remove generated artifacts
```

Tooling:

- `go` service — `golang:1.25` image, used for tests/format.
- `golint` service — `golangci/golangci-lint:v2.7-alpine`, configured via `.golangci.yml`.

The same checks run in CI via GitHub Actions (`.github/workflows/test.yml`), which also runs
`go build`, `go vet` and a secret scan that `make test` does not.

## Versions

`v3` is a rewrite with no migration path from `v2`. The two differ in every signature: `v2`
delivered through reflection to handlers of arbitrary shape, keyed topics by an unexported
string type, and reported nothing when delivery failed. `AUDIT-v3.md` records the defects that
motivated the rewrite and the reasoning behind the current design.

`v2` is frozen. It remains resolvable at `github.com/efureev/hubMessage/v2` and will not
receive further development.
