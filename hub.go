package hub

import (
	"context"
	"log/slog"
	"slices"
	"sync"
)

// Hub is a set of topics and the subscribers listening on them.
//
// The zero Hub is not usable; build one with [New]. A Hub is safe for
// concurrent use by any number of goroutines.
type Hub struct {
	mu     sync.RWMutex
	states map[topicKey]any // *topicState[T], one per (name, type)
	closed bool

	opts options

	// inflight counts events that have been accepted but not yet handled, and
	// backs Drain. workers counts running subscriber goroutines, and backs
	// Close.
	inflight *latch
	workers  *latch

	stats counters
}

// New creates a Hub.
//
// Without options a hub queues 64 events per subscriber, blocks the publisher
// when a queue is full, and reports handler failures to [slog.Default].
func New(opts ...Option) *Hub {
	o := options{
		queueSize: DefaultQueueSize,
		overflow:  Block,
		logger:    slog.Default(),
	}
	for _, apply := range opts {
		apply(&o)
	}

	return &Hub{
		states:   make(map[topicKey]any),
		opts:     o,
		inflight: newLatch(),
		workers:  newLatch(),
	}
}

// DefaultQueueSize is the per-subscriber queue depth used when [WithQueueSize]
// and [WithSubQueueSize] are not given.
const DefaultQueueSize = 64

// Topics returns a sorted, human-readable identifier for every topic that
// currently has at least one subscriber.
//
// The identifiers are for diagnostics — logs, tests, an admin endpoint. A
// topic cannot be reconstructed from one, because a name alone does not carry
// the payload type; keep the [Topic] value if you need to publish to it.
func (h *Hub) Topics() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]string, 0, len(h.states))
	for k := range h.states {
		out = append(out, k.String())
	}
	slices.Sort(out)

	return out
}

// Drain blocks until every accepted event has been handled, or ctx is done.
//
// It reports a moment at which nothing was outstanding, not a promise that
// nothing will be published afterwards: a publisher running concurrently can
// enqueue more work the instant Drain returns. Drain after the publishers have
// stopped when you need the counters to add up.
//
// Draining a hub with a stuck handler ends in ctx.Err() rather than a hang,
// which is the whole reason it takes a context.
func (h *Hub) Drain(ctx context.Context) error { return h.inflight.wait(ctx) }

// Close stops every subscriber goroutine and rejects further use: subsequent
// [Publish] and [Subscribe] calls return [ErrClosed]. Close is idempotent.
//
// Queued events are abandoned, not delivered. Call [Hub.Drain] first when they
// still matter. Close waits for handlers that are already running, so ctx
// bounds how long a slow one may hold up the shutdown.
func (h *Hub) Close(ctx context.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()

		return nil
	}
	h.closed = true
	states := make([]any, 0, len(h.states))
	for _, st := range h.states {
		states = append(states, st)
	}
	clear(h.states)
	h.mu.Unlock()

	for _, st := range states {
		if c, ok := st.(closer); ok {
			c.closeAll()
		}
	}

	return h.workers.wait(ctx)
}

// closer lets Close stop the subscribers of every topic without knowing their
// payload types: topicState[T] is stored as an any, and this is the only
// behavior Close needs from it.
type closer interface{ closeAll() }

// report hands a handler failure to the error handler and the logger. Both are
// optional; a hub configured with neither counts the failure and moves on.
func (h *Hub) report(ctx context.Context, topic string, err error) {
	if h.opts.onError != nil {
		h.opts.onError(ctx, topic, err)
	}
	if h.opts.logger != nil {
		h.opts.logger.LogAttrs(ctx, slog.LevelError, "hub: handler failed",
			slog.String("topic", topic),
			slog.String("error", err.Error()),
		)
	}
}

// latch counts outstanding work and lets waiters block on it with a context.
//
// It is a sync.WaitGroup that can be selected on. WaitGroup.Wait cannot appear
// in a select, and neither can sync.Cond.Wait, so a context-aware wait built on
// either needs a helper goroutine per waiter — one that outlives a canceled
// wait. A channel closed on the 0 transition avoids that: waiters select on it
// directly and leave nothing behind when they give up.
type latch struct {
	mu   sync.Mutex
	n    int64
	idle chan struct{} // closed exactly while n == 0
}

func newLatch() *latch {
	idle := make(chan struct{})
	close(idle)

	return &latch{idle: idle}
}

// add records n new units of outstanding work.
func (l *latch) add(n int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.n == 0 {
		// Leaving the idle state: waiters arriving from now on must block, so
		// they need an open channel to block on.
		l.idle = make(chan struct{})
	}
	l.n += n
}

// done records one unit of work as finished, waking every waiter once the
// count reaches zero.
func (l *latch) done() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.n == 0 {
		// Unbalanced done. Every add is paired with exactly one done, so this
		// is unreachable; returning keeps a future accounting slip from
		// closing an already-closed channel and panicking in a goroutine the
		// caller does not own.
		return
	}

	l.n--
	if l.n == 0 {
		close(l.idle)
	}
}

// wait blocks until the count reaches zero or ctx is done.
func (l *latch) wait(ctx context.Context) error {
	l.mu.Lock()
	idle := l.idle
	l.mu.Unlock()

	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
