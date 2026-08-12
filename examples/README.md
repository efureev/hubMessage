# msghub examples

Runnable programs that show the bus behaving over time — queues filling, events being
dropped, counters moving. Each is a self-contained `main` package; run it from the repository
root.

They complement the `Example*` functions in `example_test.go`: those show the **shape of the
API** and must print a single deterministic line, so they cannot show what happens when a
subscriber falls behind. These can.

| Example | What it demonstrates |
| --- | --- |
| [`basic`](./basic) | The five-minute tour: a topic named at run time and a type-keyed one, fan-out to two subscribers, `Drain` before `Close`, and what `Topics()` and `Snapshot()` report. |
| [`delivery`](./delivery) | The two delivery modes side by side: when the handler runs relative to `Publish` returning, where a handler error goes in each mode, per-subscriber ordering, and both kinds listening on one topic. |
| [`backpressure`](./backpressure) | The same overload against each of the four `Overflow` policies, with exact counts. `DropNewest` keeps the first events, `DropOldest` keeps the last, `Fail` refuses, `Block` paces the producer against a deadline. |
| [`failures`](./failures) | A handler that returns an error and one that panics: what the error handler receives, what the structured log records, what the counters say, and the `*PanicError` with its captured stack. |

## Running

```sh
go run ./examples/basic
go run ./examples/delivery
go run ./examples/backpressure
go run ./examples/failures
```

## Notes

**The output is deterministic.** Every run of every example prints exactly the same bytes.
That is not a happy accident of timing — the examples synchronize explicitly (a handler
signals that it has started before it blocks) so the queue state is known when the counts are
taken. Anything that would vary between runs — timestamps, pointer values in a stack trace,
the order in which independent subscribers happen to run — is stripped or sorted.

If an example ever prints different numbers on two runs, that is a bug in the example or in
the package, not noise to be ignored.

**No dependencies.** The examples import only the standard library and the package itself,
because the module carries no `require` block and must keep it that way.
