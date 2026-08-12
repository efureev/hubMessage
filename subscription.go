package hub

import (
	"context"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
)

// Handler consumes one event. Returning an error does not stop delivery to
// other subscribers; the error is reported through [WithErrorHandler] and
// [WithLogger], and — for a [Synchronous] subscription — returned to the
// publisher.
type Handler[T any] func(ctx context.Context, ev T) error

// Subscription is the handle returned by [Subscribe]. Closing it removes the
// handler and, for an asynchronous subscription, stops its goroutine.
//
// A subscription is identified by this handle and never by the handler
// function: two subscriptions of the same function, of a method value taken
// from two different receivers, or of two closures over the same literal are
// independent, and closing one leaves the others running.
type Subscription interface {
	// Close removes the handler. It is idempotent and safe to call from
	// inside the handler itself.
	Close()
	// Topic reports the topic this subscription listens on, in the same
	// human-readable form as [Hub.Topics].
	Topic() string
}

// topicState holds the subscribers of one (name, type) pair. It is stored in
// the hub's map as an any and recovered by a checked assertion, which is what
// lets a single map hold topics of unrelated payload types.
type topicState[T any] struct {
	mu   sync.RWMutex
	subs []*subscription[T]
}

type subscription[T any] struct {
	hub   *Hub
	key   topicKey
	fn    Handler[T]
	queue chan T // nil for a synchronous subscription

	policy Overflow
	sync   bool

	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	closed atomic.Bool

	// gate separates publishers from the shutdown of this subscription.
	// Publishers hold it for reading while they enqueue; stop takes it for
	// writing, which it can only do once no publisher is in the middle of one.
	// Without it a publisher could deposit an event in the buffer just after
	// the worker had gone — both cases of its select being ready at once — and
	// that event would be counted as in flight with nobody left to handle it.
	gate sync.RWMutex
}

// Subscribe registers fn as a handler for topic t and returns a handle that
// removes it.
//
// Unless [Synchronous] is given, the handler runs on its own goroutine fed by a
// bounded queue: events reach this subscriber in publication order, and a slow
// handler delays only itself until its queue fills, at which point the topic's
// [Overflow] policy decides what happens.
//
// It returns [ErrNilHub] if h is nil, [ErrNilHandler] if fn is nil and
// [ErrClosed] if the hub has been closed.
func Subscribe[T any](h *Hub, t Topic[T], fn Handler[T], opts ...SubOption) (Subscription, error) {
	if h == nil {
		return nil, ErrNilHub
	}
	if fn == nil {
		return nil, ErrNilHandler
	}
	if !t.valid() {
		return nil, ErrInvalidTopic
	}

	size, policy, isSync := h.opts.resolve(opts)

	ctx, cancel := context.WithCancel(context.Background())
	s := &subscription[T]{
		hub:    h,
		key:    t.key,
		fn:     fn,
		policy: policy,
		sync:   isSync,
		ctx:    ctx,
		cancel: cancel,
	}
	if !isSync {
		s.queue = make(chan T, size)
	}

	st, err := stateFor(h, t)
	if err != nil {
		cancel()

		return nil, err
	}

	st.mu.Lock()
	st.subs = append(st.subs, s)
	st.mu.Unlock()

	if !isSync {
		h.workers.add(1)
		go s.run()
	}

	return s, nil
}

// stateFor returns the topic's state, creating it on first subscription.
func stateFor[T any](h *Hub, t Topic[T]) (*topicState[T], error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return nil, ErrClosed
	}

	if st, ok := h.states[t.key].(*topicState[T]); ok {
		return st, nil
	}

	st := &topicState[T]{}
	h.states[t.key] = st

	return st, nil
}

// lookup returns the topic's state, or nil when nobody is subscribed.
func lookup[T any](h *Hub, t Topic[T]) (*topicState[T], error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return nil, ErrClosed
	}

	st, _ := h.states[t.key].(*topicState[T])

	return st, nil
}

// closeAll stops every subscriber of the topic. It is the [closer] half of the
// contract [Hub.Close] uses to reach a topicState whose payload type it cannot
// name. Unlike Subscription.Close it does not unlink anything: Close has
// already cleared the whole map.
func (st *topicState[T]) closeAll() {
	st.mu.Lock()
	subs := st.subs
	st.subs = nil
	st.mu.Unlock()

	for _, s := range subs {
		s.once.Do(s.stop)
	}
}

func (s *subscription[T]) Topic() string { return s.key.String() }

func (s *subscription[T]) Close() {
	s.once.Do(func() {
		s.stop()
		s.remove()
	})
}

// stop retires the subscription: it stops accepting events, wakes anything
// blocked on it, waits for publishers already inside enqueue to leave, and
// releases the accounting for whatever is still queued.
//
// The order matters. Marking and canceling first lets a publisher blocked on a
// full queue notice and give up, so the write lock is reachable. Only once it
// is held is the queue certain to be final: no publisher can add to it, and
// every event still in it is one nobody will ever handle, so Drain and Close
// must not keep waiting for it.
func (s *subscription[T]) stop() {
	s.closed.Store(true)
	s.cancel()

	s.gate.Lock()
	defer s.gate.Unlock()

	for {
		select {
		case <-s.queue: // a nil queue (synchronous subscription) selects default
			s.hub.stats.dropped.Add(1)
			s.hub.inflight.done()
		default:
			return
		}
	}
}

// remove unlinks the subscription from its topic and drops the topic entirely
// once its last subscriber leaves, so that a program using run-time topic names
// does not accumulate an empty entry per name it ever used.
func (s *subscription[T]) remove() {
	h := s.hub

	h.mu.RLock()
	st, _ := h.states[s.key].(*topicState[T])
	h.mu.RUnlock()

	if st == nil {
		return
	}

	st.mu.Lock()
	if i := slices.Index(st.subs, s); i >= 0 {
		st.subs = slices.Delete(st.subs, i, i+1)
	}
	empty := len(st.subs) == 0
	st.mu.Unlock()

	if !empty {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Re-check under the write lock: a Subscribe may have arrived in between
	// and installed a handler on this very state, or replaced it wholesale.
	cur, ok := h.states[s.key].(*topicState[T])
	if !ok || cur != st {
		return
	}

	st.mu.RLock()
	stillEmpty := len(st.subs) == 0
	st.mu.RUnlock()

	if stillEmpty {
		delete(h.states, s.key)
	}
}

// run drains the subscription's queue until it is closed.
func (s *subscription[T]) run() {
	defer s.hub.workers.done()

	for {
		select {
		case <-s.ctx.Done():
			// Whatever is still queued is released by stop, which is the only
			// thing that cancels this context and which alone can know that no
			// publisher is still filling the queue.
			return
		case ev := <-s.queue:
			// The error is already reported and counted inside invoke; the
			// worker has nowhere else to take it.
			_ = s.invoke(s.ctx, ev)
			s.hub.inflight.done()
		}
	}
}

// invoke runs the handler once, converting a panic into an error so that a
// misbehaving subscriber cannot bring down the publisher or the worker.
func (s *subscription[T]) invoke(ctx context.Context, ev T) error {
	err := s.call(ctx, ev)
	if err != nil {
		s.hub.stats.failed.Add(1)
		s.hub.report(ctx, s.key.String(), err)

		return err
	}

	s.hub.stats.delivered.Add(1)

	return nil
}

func (s *subscription[T]) call(ctx context.Context, ev T) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.hub.stats.panicked.Add(1)
			err = &PanicError{Topic: s.key.String(), Value: r, Stack: debug.Stack()}
		}
	}()

	return s.fn(ctx, ev)
}
