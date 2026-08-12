package hub

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

// quietOpt keeps the package's own tests from writing handler failures to the
// default logger, which would drown the test output.
func quietOpt() Option {
	return WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// The latch is the piece every wait in the package rests on, and its whole
// point is that a canceled wait leaves nothing behind.

func TestLatchStartsIdle(t *testing.T) {
	l := newLatch()

	if err := l.wait(t.Context()); err != nil {
		t.Fatalf("wait on a fresh latch = %v, want nil", err)
	}
}

func TestLatchWaitsForZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLatch()
		l.add(2)

		done := make(chan error, 1)
		go func() { done <- l.wait(context.Background()) }()

		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("wait returned early with %v", err)
		default:
		}

		l.done()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("wait returned at count 1 with %v", err)
		default:
		}

		l.done()
		if err := <-done; err != nil {
			t.Fatalf("wait = %v, want nil", err)
		}
	})
}

func TestLatchWaitHonoursContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLatch()
		l.add(1)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := l.wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("wait = %v, want DeadlineExceeded", err)
		}

		// A canceled wait must not leave a goroutine parked on the latch: the
		// bubble would report the leak when the test function returns. Release
		// the count so the bubble is clean either way.
		l.done()
	})
}

func TestLatchCycles(t *testing.T) {
	l := newLatch()

	for range 3 {
		l.add(1)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := l.wait(ctx); err == nil {
			t.Fatal("wait on a busy latch returned nil")
		}

		l.done()
		if err := l.wait(t.Context()); err != nil {
			t.Fatalf("wait after done = %v, want nil", err)
		}
	}
}

// An unbalanced done is unreachable through the public API; the guard exists so
// that a future accounting slip degrades instead of panicking on a closed
// channel in a goroutine the caller does not own.
func TestLatchUnbalancedDoneDoesNotPanic(t *testing.T) {
	l := newLatch()
	l.done()
	l.done()

	if err := l.wait(t.Context()); err != nil {
		t.Fatalf("wait = %v, want nil", err)
	}
}

// The hub keys topics by name and type together; these assert the key itself,
// which the black-box tests can only observe indirectly.

func TestTopicKeyIdentity(t *testing.T) {
	a := NewTopic[int]("x")
	b := NewTopic[int]("x")
	c := NewTopic[string]("x")
	d := NewTopic[int]("y")

	if a.key != b.key {
		t.Error("same name and type produced different keys")
	}
	if a.key == c.key {
		t.Error("different types collided on one key")
	}
	if a.key == d.key {
		t.Error("different names collided on one key")
	}
	if TypeTopic[int]().key != NewTopic[int]("").key {
		t.Error("TypeTopic differs from the empty-named topic")
	}
}

func TestTopicKeyString(t *testing.T) {
	if got, want := NewTopic[int]("jobs").String(), "jobs[int]"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := TypeTopic[int]().String(), "int"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	var zero Topic[int]
	if got, want := zero.String(), "hub.Topic(invalid)"; got != want {
		t.Errorf("zero Topic String() = %q, want %q", got, want)
	}
	if zero.valid() {
		t.Error("zero Topic reports itself valid")
	}
}

// The empty-topic cleanup is an internal invariant: Topics() would hide a state
// object that is still in the map but has no subscribers.
func TestStateRemovedWithLastSubscriber(t *testing.T) {
	h := New(quietOpt())

	tp := NewTopic[int]("t")
	s1, err := Subscribe(h, tp, func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Subscribe(h, tp, func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	if n := len(h.states); n != 1 {
		t.Fatalf("states = %d, want 1", n)
	}

	s1.Close()
	if n := len(h.states); n != 1 {
		t.Fatalf("states after one close = %d, want 1", n)
	}

	s2.Close()
	if n := len(h.states); n != 0 {
		t.Fatalf("states after the last close = %d, want 0", n)
	}
}

// Overflow renders itself for logs; an unknown value must stay debuggable.
func TestOverflowString(t *testing.T) {
	for _, tc := range []struct {
		p    Overflow
		want string
	}{
		{Block, "block"},
		{DropNewest, "drop-newest"},
		{DropOldest, "drop-oldest"},
		{Fail, "fail"},
		{Overflow(200), "Overflow(200)"},
	} {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("Overflow(%d).String() = %q, want %q", uint8(tc.p), got, tc.want)
		}
	}
}

func TestResolveOptions(t *testing.T) {
	base := options{queueSize: 8, overflow: Block}

	size, policy, isSync := base.resolve(nil)
	if size != 8 || policy != Block || isSync {
		t.Fatalf("defaults = %d/%v/%v", size, policy, isSync)
	}

	size, policy, isSync = base.resolve([]SubOption{
		WithSubQueueSize(0), WithSubOverflow(Fail), Synchronous(),
	})
	if size != 0 || policy != Fail || !isSync {
		t.Fatalf("overrides = %d/%v/%v", size, policy, isSync)
	}

	// A negative depth is a caller mistake, not a reason to panic in make().
	if size, _, _ := base.resolve([]SubOption{WithSubQueueSize(-5)}); size != 0 {
		t.Fatalf("negative sub queue size = %d, want 0", size)
	}
	if o := (options{}); func() int {
		WithQueueSize(-5)(&o)

		return o.queueSize
	}() != 0 {
		t.Fatal("negative hub queue size was not clamped")
	}
}
