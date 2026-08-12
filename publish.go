package msghub

import (
	"context"
	"errors"
	"fmt"
)

// Overflow selects what [Publish] does when a subscriber's queue is full.
//
// The policy is a deliberate choice about which is worse for a given stream:
// making the publisher wait, or losing an event. There is no default that is
// right for both, so the hub asks rather than picking silently.
type Overflow uint8

const (
	// Block waits for room in the queue, or for the publish context to end.
	// Nothing is lost and the producer is slowed to the speed of the slowest
	// subscriber. This is the default.
	Block Overflow = iota
	// DropNewest discards the event being published. Use it for streams where
	// a gap is preferable to a delay and the newest sample is not special —
	// progress logs, cache-warming hints.
	DropNewest
	// DropOldest evicts the oldest queued event to make room. Use it when only
	// recent events are useful: metrics, status updates, live positions.
	DropOldest
	// Fail returns [ErrQueueFull] and delivers nothing to that subscriber,
	// handing the decision back to the caller.
	Fail
)

// String implements [fmt.Stringer].
func (p Overflow) String() string {
	switch p {
	case Block:
		return "block"
	case DropNewest:
		return "drop-newest"
	case DropOldest:
		return "drop-oldest"
	case Fail:
		return "fail"
	default:
		return fmt.Sprintf("Overflow(%d)", uint8(p))
	}
}

// Publish delivers ev to every subscriber of topic t.
//
// Asynchronous subscribers are queued and handled on their own goroutines, so
// their errors are reported through [WithErrorHandler] and [WithLogger] rather
// than returned here. Subscribers registered with [Synchronous] run inline, and
// their errors are joined into the return value.
//
// Publishing to a topic nobody listens on is a no-op and returns nil, so a
// producer needs no knowledge of whether anything is subscribed.
//
// The returned error also carries whatever went wrong on the way to a queue: a
// [Fail] policy refusing a full queue, or ctx ending while a [Block] policy was
// waiting for room. One subscriber failing does not stop the others.
//
// It returns [ErrNilHub] if h is nil and [ErrClosed] if the hub has been
// closed.
func Publish[T any](ctx context.Context, h *Hub, t Topic[T], ev T) error {
	if h == nil {
		return ErrNilHub
	}
	if !t.valid() {
		return ErrInvalidTopic
	}

	st, err := lookup(h, t)
	if err != nil {
		return err
	}
	if st == nil {
		return nil
	}

	// Snapshot the subscribers and release the lock before delivering. Holding
	// it across a handler would let one slow subscriber stall every Subscribe
	// and Close on the hub, and would deadlock outright on a handler that
	// subscribes from inside its own callback.
	st.mu.RLock()
	subs := make([]*subscription[T], len(st.subs))
	copy(subs, st.subs)
	st.mu.RUnlock()

	if len(subs) == 0 {
		return nil
	}

	h.stats.published.Add(1)

	var errs []error
	for _, s := range subs {
		if s.closed.Load() {
			continue
		}

		if s.sync {
			if err := s.invoke(ctx, ev); err != nil {
				errs = append(errs, err)
			}

			continue
		}

		if err := s.enqueue(ctx, ev); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// enqueue hands ev to an asynchronous subscriber, applying its overflow policy.
//
// The in-flight count is incremented before the attempt and released again on
// every path that does not leave the event in the queue, so that Drain and
// Close never wait on an event nobody will handle.
func (s *subscription[T]) enqueue(ctx context.Context, ev T) error {
	// Held for the whole attempt, so a subscription cannot retire underneath
	// it and leave the event counted with nobody to handle it.
	s.gate.RLock()
	defer s.gate.RUnlock()

	if s.closed.Load() {
		// Retired between the snapshot and here. Not the publisher's failure,
		// and nothing to account for: the event was never accepted.
		return nil
	}

	s.hub.inflight.add(1)

	switch s.policy {
	case DropNewest:
		return s.enqueueDropNewest(ev)
	case DropOldest:
		return s.enqueueDropOldest(ev)
	case Fail:
		return s.enqueueFail(ev)
	default:
		return s.enqueueBlock(ctx, ev)
	}
}

func (s *subscription[T]) enqueueBlock(ctx context.Context, ev T) error {
	select {
	case s.queue <- ev:
		return nil
	case <-ctx.Done():
		s.drop()

		return fmt.Errorf("%s: %w", s.key, ctx.Err())
	case <-s.ctx.Done():
		// The subscription closed while we waited. It is no longer a
		// subscriber, so this is not the publisher's failure.
		s.drop()

		return nil
	}
}

func (s *subscription[T]) enqueueDropNewest(ev T) error {
	select {
	case s.queue <- ev:
		return nil
	default:
		s.drop()

		return nil
	}
}

// enqueueDropOldest evicts queued events until the new one fits.
//
// The loop is bounded by the queue depth: concurrent publishers refilling the
// queue must not be able to keep one producer evicting forever. Once the budget
// runs out the event is dropped, which is the policy's own contract applied to
// itself.
func (s *subscription[T]) enqueueDropOldest(ev T) error {
	budget := cap(s.queue) + 1

	for range budget {
		select {
		case s.queue <- ev:
			return nil
		default:
		}

		select {
		case <-s.queue:
			s.hub.stats.dropped.Add(1)
			s.hub.inflight.done()
		default:
			// Someone else drained it; try to send again.
		}
	}

	s.drop()

	return nil
}

func (s *subscription[T]) enqueueFail(ev T) error {
	select {
	case s.queue <- ev:
		return nil
	default:
		s.drop()

		return fmt.Errorf("%s: %w", s.key, ErrQueueFull)
	}
}

// drop accounts for an event that will never reach a handler.
func (s *subscription[T]) drop() {
	s.hub.stats.dropped.Add(1)
	s.hub.inflight.done()
}
