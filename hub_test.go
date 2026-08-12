// Package hub_test exercises the bus exactly as a consumer sees it.
//
// This is the half of the suite v2 did not have. Its tests lived inside the
// package, where the unexported topic type was nameable, so 97% coverage said
// nothing about whether an outside caller could even compile a call. Every test
// here goes through the exported API only; a regression that breaks consumers
// breaks this file.
package hub_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	hub "github.com/efureev/hubMessage/v3"
)

// UserCreated is an ordinary domain event, declared outside the package like a
// consumer's would be.
type UserCreated struct{ ID string }

func quiet() hub.Option {
	return hub.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newHub(t *testing.T, opts ...hub.Option) *hub.Hub {
	t.Helper()

	h := hub.New(append([]hub.Option{quiet()}, opts...)...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := h.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return h
}

// --------------------------------------------------------------- basics

func TestPublishSubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.NewTopic[UserCreated]("user.created")

		var got UserCreated
		sub, err := hub.Subscribe(h, topic, func(_ context.Context, ev UserCreated) error {
			got = ev

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		if err := hub.Publish(t.Context(), h, topic, UserCreated{ID: "42"}); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if got.ID != "42" {
			t.Fatalf("handler saw %+v", got)
		}
	})
}

// A consumer must be able to name a topic it computes at run time. In v2 this
// did not compile outside the package, and no test could tell: the topic type
// was unexported, so only string literals worked.
func TestTopicNameComputedAtRunTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())

		var mu sync.Mutex
		seen := map[string]int{}

		for i := range 3 {
			name := fmt.Sprintf("job.%d", i) // a variable, not a literal
			topic := hub.NewTopic[int](name)

			sub, err := hub.Subscribe(h, topic, func(_ context.Context, v int) error {
				mu.Lock()
				defer mu.Unlock()
				seen[name] = v

				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()

			if err := hub.Publish(t.Context(), h, topic, i*10); err != nil {
				t.Fatal(err)
			}
		}

		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()
		for i := range 3 {
			name := fmt.Sprintf("job.%d", i)
			if seen[name] != i*10 {
				t.Errorf("%s = %d, want %d", name, seen[name], i*10)
			}
		}
	})
}

// The same name with two payload types is two streams, not a collision.
func TestNameAndTypeTogetherKeyTheTopic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())

		ints := hub.NewTopic[int]("x")
		strs := hub.NewTopic[string]("x")

		var gotInt, gotStr atomic.Int32
		si, err := hub.Subscribe(h, ints, func(context.Context, int) error { gotInt.Add(1); return nil })
		if err != nil {
			t.Fatal(err)
		}
		defer si.Close()
		ss, err := hub.Subscribe(h, strs, func(context.Context, string) error { gotStr.Add(1); return nil })
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()

		if err := hub.Publish(t.Context(), h, ints, 1); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if gotInt.Load() != 1 || gotStr.Load() != 0 {
			t.Fatalf("int=%d str=%d, want 1/0", gotInt.Load(), gotStr.Load())
		}
	})
}

func TestTypeTopic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.TypeTopic[UserCreated]()

		var got atomic.Int32
		sub, err := hub.Subscribe(h, topic, func(context.Context, UserCreated) error {
			got.Add(1)

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		if err := hub.Publish(t.Context(), h, topic, UserCreated{ID: "1"}); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if got.Load() != 1 {
			t.Fatalf("delivered %d, want 1", got.Load())
		}
		if name := topic.Name(); name != "" {
			t.Errorf("TypeTopic name = %q, want empty", name)
		}
	})
}

func TestPublishWithoutSubscribers(t *testing.T) {
	h := newHub(t)

	if err := hub.Publish(t.Context(), h, hub.NewTopic[int]("nobody"), 1); err != nil {
		t.Fatalf("publish to an empty topic = %v, want nil", err)
	}
	if got := h.Snapshot(); got != (hub.Stats{}) {
		t.Fatalf("counters moved on an unheard publish: %+v", got)
	}
}

func TestTopicsListsSubscribedTopics(t *testing.T) {
	h := newHub(t)

	subA, err := hub.Subscribe(h, hub.NewTopic[int]("b"), func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	subB, err := hub.Subscribe(h, hub.TypeTopic[UserCreated](), func(context.Context, UserCreated) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	// Topics() is sorted, so the named topic sorts before the type-only one.
	got := h.Topics()
	if len(got) != 2 {
		t.Fatalf("Topics() = %v, want 2 entries", got)
	}
	if got[0] != "b[int]" {
		t.Errorf("Topics()[0] = %q, want %q", got[0], "b[int]")
	}
	if !strings.HasSuffix(got[1], "UserCreated") {
		t.Errorf("Topics()[1] = %q, want the type-only topic", got[1])
	}

	subA.Close()
	subB.Close()

	if got := h.Topics(); len(got) != 0 {
		t.Fatalf("Topics() after closing everything = %v, want empty", got)
	}
}

// --------------------------------------------------------------- v2 regressions
//
// One test per defect confirmed in AUDIT-v3.md §4. Each one hangs, drops a
// message or removes the wrong handler on v2.

// D-1: a handler publishing to its own topic deadlocked forever — the queue was
// a rendezvous and the only receiver was the handler itself.
func TestHandlerCanPublishToItsOwnTopic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.NewTopic[int]("recursive")

		var depth atomic.Int32
		sub, err := hub.Subscribe(h, topic, func(ctx context.Context, v int) error {
			if v < 3 {
				depth.Store(int32(v) + 1)

				return hub.Publish(ctx, h, topic, v+1)
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		if err := hub.Publish(t.Context(), h, topic, 0); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if got := depth.Load(); got != 3 {
			t.Fatalf("recursion reached %d, want 3", got)
		}
	})
}

// D-2: Publish blocked behind a busy subscriber from the second event onwards,
// while the Readme promised it never blocked the producer.
func TestPublisherIsNotBlockedWithinQueueDepth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(16))
		topic := hub.NewTopic[int]("slow")

		release := make(chan struct{})
		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			<-release

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		published := make(chan struct{})
		go func() {
			defer close(published)
			for i := range 10 {
				_ = hub.Publish(context.Background(), h, topic, i)
			}
		}()

		// synctest.Wait blocks until every other goroutine in the bubble is
		// durably blocked. If the publisher were stuck behind the handler, the
		// channel would still be open here.
		synctest.Wait()
		select {
		case <-published:
		default:
			close(release)
			t.Fatal("publisher blocked behind a busy subscriber within the queue depth")
		}

		close(release)
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

// D-3: unsubscribing went by function pointer, so a method value taken from two
// different receivers was indistinguishable and closing one closed both.
func TestSubscriptionsAreIndependentOfTheHandlerValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.NewTopic[int]("t")

		a, b := &counterService{}, &counterService{}

		subA, err := hub.Subscribe(h, topic, a.OnEvent)
		if err != nil {
			t.Fatal(err)
		}
		subB, err := hub.Subscribe(h, topic, b.OnEvent)
		if err != nil {
			t.Fatal(err)
		}
		defer subB.Close()

		subA.Close()

		if err := hub.Publish(t.Context(), h, topic, 1); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if a.n.Load() != 0 {
			t.Errorf("closed subscription still received %d events", a.n.Load())
		}
		if b.n.Load() != 1 {
			t.Errorf("the other receiver got %d events, want 1", b.n.Load())
		}
	})
}

type counterService struct{ n atomic.Int32 }

func (s *counterService) OnEvent(context.Context, int) error {
	s.n.Add(1)

	return nil
}

// The same function value subscribed twice is two subscriptions, and closing
// one leaves the other. v2 removed both.
func TestSameFunctionSubscribedTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.NewTopic[int]("t")

		var n atomic.Int32
		fn := func(context.Context, int) error { n.Add(1); return nil }

		s1, err := hub.Subscribe(h, topic, fn)
		if err != nil {
			t.Fatal(err)
		}
		s2, err := hub.Subscribe(h, topic, fn)
		if err != nil {
			t.Fatal(err)
		}
		defer s2.Close()

		s1.Close()

		if err := hub.Publish(t.Context(), h, topic, 1); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if n.Load() != 1 {
			t.Fatalf("handler ran %d times, want 1", n.Load())
		}
	})
}

// D-4: a nil argument produced an invalid reflect.Value, the call panicked and
// the panic was swallowed — the event vanished. A nil error is now a value.
func TestNilPayloadIsDelivered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.NewTopic[error]("errors")

		var called atomic.Bool
		var got error
		sub, err := hub.Subscribe(h, topic, func(_ context.Context, e error) error {
			called.Store(true)
			got = e

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		if err := hub.Publish(t.Context(), h, topic, nil); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if !called.Load() {
			t.Fatal("a nil payload was dropped")
		}
		if got != nil {
			t.Fatalf("handler saw %v, want nil", got)
		}
	})
}

// D-5: every topic name ever used stayed in the map, so run-time names leaked
// memory and Topics() reported streams with no subscribers.
func TestClosingTheLastSubscriberDropsTheTopic(t *testing.T) {
	h := newHub(t)

	subs := make([]hub.Subscription, 0, 100)
	for i := range 100 {
		topic := hub.NewTopic[int](fmt.Sprintf("job.%d", i))
		s, err := hub.Subscribe(h, topic, func(context.Context, int) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, s)
	}

	if n := len(h.Topics()); n != 100 {
		t.Fatalf("Topics() = %d, want 100", n)
	}

	for _, s := range subs {
		s.Close()
	}

	if got := h.Topics(); len(got) != 0 {
		t.Fatalf("%d topics retained after every subscriber left", len(got))
	}
}

// D-6: a failing handler was invisible — no error, no log, no counter.
func TestHandlerFailuresAreReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var seen []error

		h := hub.New(quiet(), hub.WithErrorHandler(func(_ context.Context, _ string, err error) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, err)
		}))

		topic := hub.NewTopic[int]("t")
		boom := errors.New("boom")

		s1, err := hub.Subscribe(h, topic, func(context.Context, int) error { return boom })
		if err != nil {
			t.Fatal(err)
		}
		defer s1.Close()
		s2, err := hub.Subscribe(h, topic, func(context.Context, int) error { panic("kaboom") })
		if err != nil {
			t.Fatal(err)
		}
		defer s2.Close()

		if err := hub.Publish(t.Context(), h, topic, 1); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()

		if len(seen) != 2 {
			t.Fatalf("error handler saw %d failures, want 2", len(seen))
		}
		if !errors.Is(errors.Join(seen...), boom) {
			t.Error("the returned error was not reported")
		}

		var pe *hub.PanicError
		if !errors.As(errors.Join(seen...), &pe) {
			t.Fatal("the panic was not reported as a PanicError")
		}
		if len(pe.Stack) == 0 {
			t.Error("PanicError carries no stack")
		}
		if pe.Value != "kaboom" {
			t.Errorf("PanicError.Value = %v, want kaboom", pe.Value)
		}

		if got := h.Snapshot(); got.Failed != 2 || got.Panicked != 1 || got.Delivered != 0 {
			t.Errorf("counters = %+v, want Failed 2 / Panicked 1 / Delivered 0", got)
		}
	})
}

// A panic value that is an error stays reachable through errors.Is.
func TestPanicErrorUnwrapsAnErrorValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("boom")
		h := hub.New(quiet())
		topic := hub.NewTopic[int]("t")

		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error { panic(boom) },
			hub.Synchronous())
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		err = hub.Publish(t.Context(), h, topic, 1)
		if !errors.Is(err, boom) {
			t.Fatalf("Publish = %v, want it to wrap boom", err)
		}
	})
}

// --------------------------------------------------------------- delivery modes

func TestSynchronousHandlerReturnsItsErrorToThePublisher(t *testing.T) {
	h := newHub(t)
	topic := hub.NewTopic[int]("t")
	boom := errors.New("boom")

	sub, err := hub.Subscribe(h, topic, func(context.Context, int) error { return boom },
		hub.Synchronous())
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if err := hub.Publish(t.Context(), h, topic, 1); !errors.Is(err, boom) {
		t.Fatalf("Publish = %v, want boom", err)
	}
}

// A synchronous handler has finished by the time Publish returns; an
// asynchronous one has not, and its error does not travel back.
func TestSynchronousRunsInline(t *testing.T) {
	h := newHub(t)
	topic := hub.NewTopic[int]("t")

	var order []string
	var mu sync.Mutex

	sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "handler")

		return nil
	}, hub.Synchronous())
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if err := hub.Publish(t.Context(), h, topic, 1); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	order = append(order, "publish returned")
	got := strings.Join(order, ",")
	mu.Unlock()

	if got != "handler,publish returned" {
		t.Fatalf("order = %q", got)
	}
}

func TestAsynchronousErrorDoesNotReachThePublisher(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.NewTopic[int]("t")

		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			return errors.New("boom")
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		if err := hub.Publish(t.Context(), h, topic, 1); err != nil {
			t.Fatalf("Publish = %v, want nil", err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := h.Snapshot(); got.Failed != 1 {
			t.Fatalf("Failed = %d, want 1", got.Failed)
		}
	})
}

// Events reach one subscriber in publication order.
func TestPerSubscriberOrdering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(128))
		topic := hub.NewTopic[int]("t")

		var mu sync.Mutex
		var got []int

		sub, err := hub.Subscribe(h, topic, func(_ context.Context, v int) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, v)

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		for i := range 50 {
			if err := hub.Publish(t.Context(), h, topic, i); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()
		for i, v := range got {
			if v != i {
				t.Fatalf("event %d arrived at position %d", v, i)
			}
		}
		if len(got) != 50 {
			t.Fatalf("received %d events, want 50", len(got))
		}
	})
}

// --------------------------------------------------------------- backpressure

func TestOverflowDropNewest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(2), hub.WithOverflow(hub.DropNewest))
		topic := hub.NewTopic[int]("t")

		release := make(chan struct{})
		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			<-release

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		for i := range 20 {
			if err := hub.Publish(t.Context(), h, topic, i); err != nil {
				t.Fatalf("DropNewest returned %v, want nil", err)
			}
		}

		close(release)
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		got := h.Snapshot()
		if got.Dropped == 0 {
			t.Fatal("nothing was dropped with a full queue")
		}
		if got.Delivered+got.Dropped != 20 {
			t.Fatalf("delivered %d + dropped %d != 20", got.Delivered, got.Dropped)
		}
	})
}

func TestOverflowDropOldestKeepsTheLatest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(2), hub.WithOverflow(hub.DropOldest))
		topic := hub.NewTopic[int]("t")

		release := make(chan struct{})
		var mu sync.Mutex
		var got []int

		sub, err := hub.Subscribe(h, topic, func(_ context.Context, v int) error {
			<-release
			mu.Lock()
			defer mu.Unlock()
			got = append(got, v)

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		for i := range 20 {
			if err := hub.Publish(t.Context(), h, topic, i); err != nil {
				t.Fatalf("DropOldest returned %v, want nil", err)
			}
		}

		close(release)
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()

		if len(got) == 0 {
			t.Fatal("nothing was delivered")
		}
		// The newest event is the one the policy protects.
		if last := got[len(got)-1]; last != 19 {
			t.Errorf("last delivered = %d, want 19", last)
		}
	})
}

func TestOverflowFail(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(1), hub.WithOverflow(hub.Fail))
		topic := hub.NewTopic[int]("t")

		release := make(chan struct{})
		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			<-release

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		var failures int
		for i := range 10 {
			if err := hub.Publish(t.Context(), h, topic, i); err != nil {
				if !errors.Is(err, hub.ErrQueueFull) {
					t.Fatalf("Publish = %v, want ErrQueueFull", err)
				}
				failures++
			}
		}

		if failures == 0 {
			t.Fatal("Fail policy never reported a full queue")
		}

		close(release)
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

// Block is the default: the publisher waits rather than losing an event, and a
// context gives it a way out.
func TestOverflowBlockHonoursThePublishContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(1))
		topic := hub.NewTopic[int]("t")

		release := make(chan struct{})
		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			<-release

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		var err2 error
		for range 10 {
			if err2 = hub.Publish(ctx, h, topic, 1); err2 != nil {
				break
			}
		}

		if !errors.Is(err2, context.DeadlineExceeded) {
			t.Fatalf("Publish = %v, want DeadlineExceeded", err2)
		}

		close(release)
	})
}

// A per-subscription policy overrides the hub default.
func TestPerSubscriptionOverrides(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(64), hub.WithOverflow(hub.Block))
		topic := hub.NewTopic[int]("t")

		release := make(chan struct{})
		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			<-release

			return nil
		}, hub.WithSubQueueSize(1), hub.WithSubOverflow(hub.Fail))
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		var sawFull bool
		for range 10 {
			if err := hub.Publish(t.Context(), h, topic, 1); errors.Is(err, hub.ErrQueueFull) {
				sawFull = true

				break
			}
		}

		if !sawFull {
			t.Fatal("the subscription override was ignored")
		}

		close(release)
	})
}

// --------------------------------------------------------------- lifecycle

func TestDrainReportsADeadlineInsteadOfHanging(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.NewTopic[int]("t")

		release := make(chan struct{})
		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			<-release

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		if err := hub.Publish(t.Context(), h, topic, 1); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := h.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Drain = %v, want DeadlineExceeded", err)
		}

		close(release)
		if err := h.Drain(t.Context()); err != nil {
			t.Fatalf("Drain after release = %v, want nil", err)
		}
	})
}

func TestCloseIsIdempotentAndRejectsFurtherUse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.NewTopic[int]("t")

		for range 20 {
			if _, err := hub.Subscribe(h, topic, func(context.Context, int) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}

		if err := h.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := h.Close(t.Context()); err != nil {
			t.Fatalf("second Close = %v, want nil", err)
		}

		if err := hub.Publish(t.Context(), h, topic, 1); !errors.Is(err, hub.ErrClosed) {
			t.Errorf("Publish after Close = %v, want ErrClosed", err)
		}
		if _, err := hub.Subscribe(h, topic, func(context.Context, int) error { return nil }); !errors.Is(err, hub.ErrClosed) {
			t.Errorf("Subscribe after Close = %v, want ErrClosed", err)
		}
		if got := h.Topics(); len(got) != 0 {
			t.Errorf("Topics() after Close = %v, want empty", got)
		}
	})
}

// Close must not wait forever on events nobody will handle any more.
func TestCloseAbandonsQueuedEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(32))
		topic := hub.NewTopic[int]("t")

		release := make(chan struct{})
		if _, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			<-release

			return nil
		}); err != nil {
			t.Fatal(err)
		}

		for i := range 10 {
			if err := hub.Publish(t.Context(), h, topic, i); err != nil {
				t.Fatal(err)
			}
		}

		close(release)
		if err := h.Close(t.Context()); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		// The abandoned events must not leave Drain waiting on them.
		if err := h.Drain(t.Context()); err != nil {
			t.Fatalf("Drain after Close = %v, want nil", err)
		}
	})
}

func TestCloseFromInsideAHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		topic := hub.NewTopic[int]("t")

		var sub hub.Subscription
		var err error
		sub, err = hub.Subscribe(h, topic, func(context.Context, int) error {
			sub.Close() // idempotent, and must not deadlock against the worker

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		if err := hub.Publish(t.Context(), h, topic, 1); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if got := h.Topics(); len(got) != 0 {
			t.Fatalf("Topics() = %v, want empty", got)
		}
	})
}

// Subscribing from inside a handler needs the hub's write lock while a publish
// is in flight; holding the lock across delivery would deadlock.
func TestSubscribeFromInsideAHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet())
		first := hub.NewTopic[int]("first")
		second := hub.NewTopic[int]("second")

		done := make(chan struct{})
		sub, err := hub.Subscribe(h, first, func(context.Context, int) error {
			s, err := hub.Subscribe(h, second, func(context.Context, int) error { return nil })
			if err != nil {
				return err
			}
			s.Close()
			close(done)

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		if err := hub.Publish(t.Context(), h, first, 1); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		select {
		case <-done:
		default:
			t.Fatal("the nested Subscribe never completed")
		}
	})
}

// --------------------------------------------------------------- misuse

func TestNilAndZeroArguments(t *testing.T) {
	h := newHub(t)
	topic := hub.NewTopic[int]("t")
	var zero hub.Topic[int]

	if _, err := hub.Subscribe(h, topic, nil); !errors.Is(err, hub.ErrNilHandler) {
		t.Errorf("Subscribe(nil handler) = %v, want ErrNilHandler", err)
	}
	if _, err := hub.Subscribe[int](nil, topic, func(context.Context, int) error { return nil }); !errors.Is(err, hub.ErrNilHub) {
		t.Errorf("Subscribe(nil hub) = %v, want ErrNilHub", err)
	}
	if err := hub.Publish[int](t.Context(), nil, topic, 1); !errors.Is(err, hub.ErrNilHub) {
		t.Errorf("Publish(nil hub) = %v, want ErrNilHub", err)
	}
	if _, err := hub.Subscribe(h, zero, func(context.Context, int) error { return nil }); !errors.Is(err, hub.ErrInvalidTopic) {
		t.Errorf("Subscribe(zero topic) = %v, want ErrInvalidTopic", err)
	}
	if err := hub.Publish(t.Context(), h, zero, 1); !errors.Is(err, hub.ErrInvalidTopic) {
		t.Errorf("Publish(zero topic) = %v, want ErrInvalidTopic", err)
	}
}

// A hub with neither logger nor error handler still counts failures.
func TestSilentHubStillCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(hub.WithLogger(nil))
		topic := hub.NewTopic[int]("t")

		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			return errors.New("boom")
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		if err := hub.Publish(t.Context(), h, topic, 1); err != nil {
			t.Fatal(err)
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if got := h.Snapshot(); got.Failed != 1 {
			t.Fatalf("Failed = %d, want 1", got.Failed)
		}
	})
}

// A rendezvous queue is legal and delivers; it just makes Publish wait.
func TestZeroQueueSize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(0))
		topic := hub.NewTopic[int]("t")

		var n atomic.Int32
		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			n.Add(1)

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		for range 5 {
			if err := hub.Publish(t.Context(), h, topic, 1); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if n.Load() != 5 {
			t.Fatalf("delivered %d, want 5", n.Load())
		}
	})
}

// --------------------------------------------------------------- concurrency

func TestConcurrentPublishSubscribeCloseDrain(t *testing.T) {
	h := newHub(t, hub.WithQueueSize(512))
	topic := hub.NewTopic[int]("t")

	var delivered atomic.Int64
	sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
		delivered.Add(1)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	const publishers, perPublisher = 8, 100

	var wg sync.WaitGroup
	wg.Add(publishers * 2)

	for range publishers {
		go func() {
			defer wg.Done()
			for i := range perPublisher {
				if err := hub.Publish(context.Background(), h, topic, i); err != nil {
					t.Errorf("Publish: %v", err)

					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			// Churn short-lived subscriptions against the same topic.
			for range 20 {
				s, err := hub.Subscribe(h, topic, func(context.Context, int) error { return nil })
				if err != nil {
					t.Errorf("Subscribe: %v", err)

					return
				}
				s.Close()
			}
		}()
	}

	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	if got := delivered.Load(); got != publishers*perPublisher {
		t.Fatalf("delivered %d, want %d", got, publishers*perPublisher)
	}
}

func TestConcurrentDrainers(t *testing.T) {
	h := newHub(t, hub.WithQueueSize(256))
	topic := hub.NewTopic[int]("t")

	sub, err := hub.Subscribe(h, topic, func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	var wg sync.WaitGroup
	wg.Add(20)

	for range 10 {
		go func() {
			defer wg.Done()
			for i := range 50 {
				_ = hub.Publish(context.Background(), h, topic, i)
			}
		}()
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := h.Drain(ctx); err != nil {
				t.Errorf("Drain: %v", err)
			}
		}()
	}

	wg.Wait()
}

// Subscription.Topic reports the stream, in the same form as Hub.Topics.
func TestSubscriptionTopic(t *testing.T) {
	h := newHub(t)

	named, err := hub.Subscribe(h, hub.NewTopic[int]("jobs"), func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer named.Close()

	typed, err := hub.Subscribe(h, hub.TypeTopic[UserCreated](), func(context.Context, UserCreated) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer typed.Close()

	if got, want := named.Topic(), "jobs[int]"; got != want {
		t.Errorf("Topic() = %q, want %q", got, want)
	}
	if got := typed.Topic(); !strings.HasSuffix(got, "UserCreated") {
		t.Errorf("Topic() = %q, want the bare type", got)
	}
}

// Closing a subscription twice, and closing one whose topic is already gone,
// must both be no-ops rather than corrupting the topic map.
func TestCloseSubscriptionTwice(t *testing.T) {
	h := newHub(t)
	topic := hub.NewTopic[int]("t")

	sub, err := hub.Subscribe(h, topic, func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	sub.Close()
	sub.Close()

	if got := h.Topics(); len(got) != 0 {
		t.Fatalf("Topics() = %v, want empty", got)
	}

	// A fresh subscription on the same topic must still work afterwards.
	again, err := hub.Subscribe(h, topic, func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()

	if got := h.Topics(); len(got) != 1 {
		t.Fatalf("Topics() = %v, want one entry", got)
	}
}

// DropOldest on a rendezvous queue has no buffer to evict from, so the policy
// applies to itself: the event is dropped rather than blocking the publisher.
func TestOverflowDropOldestWithoutBuffer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := hub.New(quiet(), hub.WithQueueSize(0), hub.WithOverflow(hub.DropOldest))
		topic := hub.NewTopic[int]("t")

		release := make(chan struct{})
		sub, err := hub.Subscribe(h, topic, func(context.Context, int) error {
			<-release

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()

		for i := range 5 {
			if err := hub.Publish(t.Context(), h, topic, i); err != nil {
				t.Fatalf("Publish = %v, want nil", err)
			}
		}

		close(release)
		if err := h.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}

		if got := h.Snapshot(); got.Dropped == 0 {
			t.Fatal("nothing was dropped on a rendezvous queue")
		}
	})
}

// Publishing into a subscription that retires mid-flight is not the
// publisher's failure and must not leave the event counted as in flight.
func TestPublishRacesSubscriptionClose(t *testing.T) {
	h := newHub(t, hub.WithQueueSize(4))
	topic := hub.NewTopic[int]("t")

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for range 200 {
			s, err := hub.Subscribe(h, topic, func(context.Context, int) error { return nil })
			if err != nil {
				t.Errorf("Subscribe: %v", err)

				return
			}
			s.Close()
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 200 {
			if err := hub.Publish(context.Background(), h, topic, i); err != nil {
				t.Errorf("Publish: %v", err)

				return
			}
		}
	}()

	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v — an event was counted with nobody to handle it", err)
	}
}
