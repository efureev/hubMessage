package hub

import "sync/atomic"

// Stats is a snapshot of a hub's delivery counters, as returned by
// [Hub.Snapshot]. It is a plain value: reading it involves no locking and it
// does not change after it is taken.
type Stats struct {
	// Published counts calls to Publish that reached a topic with at least one
	// subscriber.
	Published uint64
	// Delivered counts handler invocations that returned without an error.
	Delivered uint64
	// Dropped counts events that never reached a handler: discarded or evicted
	// by an overflow policy, refused because the queue was full, or abandoned
	// because the publish context ended or the subscription closed first.
	Dropped uint64
	// Panicked counts handler invocations that panicked. Every panic is also
	// counted in Failed.
	Panicked uint64
	// Failed counts handler invocations that reported an error, whether the
	// handler returned it or panicked.
	Failed uint64
}

// counters is the mutable form of [Stats]. It is kept separate so the public
// snapshot can be copied and compared like an ordinary struct, which
// atomic.Uint64 fields would forbid.
type counters struct {
	published atomic.Uint64
	delivered atomic.Uint64
	dropped   atomic.Uint64
	panicked  atomic.Uint64
	failed    atomic.Uint64
}

func (c *counters) snapshot() Stats {
	return Stats{
		Published: c.published.Load(),
		Delivered: c.delivered.Load(),
		Dropped:   c.dropped.Load(),
		Panicked:  c.panicked.Load(),
		Failed:    c.failed.Load(),
	}
}

// Snapshot returns the hub's delivery counters.
//
// The counters are read independently, so a snapshot taken while events are in
// flight is a close reading rather than a consistent cut. Take it after
// [Hub.Drain] when the numbers have to add up exactly.
func (h *Hub) Snapshot() Stats { return h.stats.snapshot() }
