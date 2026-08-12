# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/).

## [v3.0.0]

A rewrite. The module path becomes `github.com/efureev/hubMessage/v3`; there is no migration
path from v2, because every signature changed. `AUDIT-v3.md` records the defects that
motivated it, each reproduced by a probe, and the reasoning behind the current design.

### Changed — breaking

- **Topics are typed values.** `hub.NewTopic[T](name)` returns an exported `Topic[T]` keyed by
  name *and* payload type. In v2 `topic` was an unexported string type appearing in exported
  signatures, so an outside caller could pass a string literal but not a variable: computing a
  topic name at run time did not compile. The package-level `Sub`/`Event` helpers existed only
  to work around this, and only for the singleton.

  A name with two payload types is two independent streams. `hub.TypeTopic[T]()` keys by the
  type alone.

- **Handlers are typed and take a context**: `func(ctx context.Context, ev T) error` instead
  of a function of arbitrary shape invoked through reflection. A signature mismatch is now a
  compile error rather than a message that vanishes at run time.

- **`Publish` and `Subscribe` are package functions**, not methods, and take the hub as their
  first argument. Go has no generic methods.

- **Unsubscribing is by handle.** `Subscribe` returns a `Subscription`; `Unsubscribe(fn)` is
  gone. v2 compared `reflect.Value.Pointer()`, which is the same for a method value taken from
  two different receivers — so unsubscribing one instance's handler silently removed another's.

- **Delivery is queued and bounded.** Each subscriber owns a buffered queue whose depth is
  configurable. v2 used an unbounded-in-time rendezvous: the effective queue depth was one, so
  `Publish` blocked behind a busy subscriber from the second event onwards, and a handler
  publishing to its own topic deadlocked forever.

- **`Wait()` is replaced by `Drain(ctx)` and `Close(ctx)`.** Both take a context, so a stuck
  handler ends in a deadline instead of a hang.

- **The global singleton is gone**: `Get`, `Sub`, `Event` and `Reset` are not carried over.
  Global mutable state in a library is imposed on every consumer and makes tests order
  dependent. An application that wants one writes `var Bus = hub.New()`.

- **The `appmod` dependency is gone.** The bus no longer embeds `appmod.AppModule`, so its
  public API is not hostage to another module's major version, and importing the bus no longer
  pulls a lifecycle framework into the consumer's module graph. Lifecycle integration lives in
  `github.com/efureev/appmod/adapters/hubmod`.

- **Go 1.25** is the minimum, up from 1.24.

### Added

- **`Synchronous()`** runs a handler inline in `Publish` and returns its error to the
  publisher, for handlers that must complete — or fail — before the publisher proceeds.

- **Overflow policies** for a full queue: `Block` (default), `DropNewest`, `DropOldest`,
  `Fail`. Settable per hub and overridable per subscription. v2 had no policy: it blocked, for
  as long as it took.

- **Failure reporting.** `WithErrorHandler` and `WithLogger` receive every handler error and
  every recovered panic; `Hub.Snapshot()` counts published, delivered, dropped, panicked and
  failed. In v2 a panic — including the one caused by every signature mismatch — was recovered
  and discarded with no log, no error and no counter.

- **`PanicError`** carries the recovered value and the stack captured at recovery, and unwraps
  to the panic value when that value is an error.

- **`Hub.Topics()`** reports the topics that currently have subscribers. Closing the last
  subscription drops the topic entirely; v2 kept every name it had ever seen, which leaked
  memory for run-time topic names.

### Fixed

Each of these is covered by a regression test in `hub_test.go`:

- A handler publishing to its own topic no longer deadlocks.
- A busy subscriber no longer blocks the publisher within the queue depth.
- Closing one subscription no longer removes another that shares a function pointer.
- A `nil` payload is delivered as a value instead of vanishing. In v2 `reflect.ValueOf(nil)`
  produced an invalid value, the call panicked and the panic was swallowed.
- Topics with no subscribers no longer accumulate.

### Internal

- The test suite gained a black-box `hub_test` package. v2's tests all lived inside the
  package, where the unexported topic type was nameable, so 97% coverage said nothing about
  whether an outside caller could compile a call at all.
- Concurrency tests use `testing/synctest`, replacing timeout-based waits that were sensitive
  to scheduling on the macOS runner.
