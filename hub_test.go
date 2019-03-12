package hub

import (
	"errors"
	"sync"
	"testing"
)
import . "github.com/smartystreets/goconvey/convey"

func TestNew(t *testing.T) {
	h := New()

	if h == nil {
		t.Fail()
	}
}

func TestGet(t *testing.T) {
	h := Get()

	if h == nil {
		t.Fail()
	}
}

func TestSubscribe(t *testing.T) {
	h := Get()

	handler := func() {}

	if h.Subscribe("test", handler) != nil {
		t.Fail()
	}

	if h.Subscribe("test", 2) == nil {
		t.Fail()
	}

	if h.Subscribe("test", handler) != nil {
		t.Fail()
	}
}

func TestPublish(t *testing.T) {
	h := New()

	var wg sync.WaitGroup
	wg.Add(2)

	first := false
	second := false

	h.Subscribe("topic", func(v bool) {
		defer wg.Done()
		first = v
	})

	h.Subscribe("topic", func(v bool) {
		defer wg.Done()
		second = v
	})

	h.Publish("topic", true)

	wg.Wait()

	if first == false || second == false {
		t.Fail()
	}
}

func TestHandleError(t *testing.T) {
	h := New()

	h.Subscribe("topic", func(out chan<- error) {
		out <- errors.New("I do throw error")
	})

	out := make(chan error)
	defer close(out)

	h.Publish("topic", out)

	if <-out == nil {
		t.Fail()
	}
}

func TestHub(t *testing.T) {
	Convey("Hub", t, func() {
		h := Get()

		Convey("Base", func() {
			So(h, ShouldHaveSameTypeAs, &hub{})
		})

		Convey("Config", func() {
			So(h.Config(), ShouldHaveSameTypeAs, Config{})
			So(h.Config().Name(), ShouldEqual, `Hub`)
			So(h.Config().Version(), ShouldEqual, `v0.0.1`)
		})

		Convey("Unsubscribe", func() {
			handler := func() {}

			if h.Subscribe("test", handler) != nil {
				t.Fail()
			}

			err := h.Unsubscribe("test", handler)
			So(err, ShouldBeNil)

			err = h.Unsubscribe("test", handler)
			So(err, ShouldBeNil)

			err = h.Unsubscribe("unexisted", handler)
			So(err, ShouldBeError)

		})

		Convey("Close", func() {
			handler := func() {}

			if h.Subscribe("test", handler) != nil {
				t.Fail()
			}

			original, ok := h.(*hub)

			Convey("Cast message bus to its original type", func() {
				So(ok, ShouldBeTrue)
			})

			Convey("Subscribed handler to topic", func() {
				So(len(original.channels), ShouldEqual, 1)
			})

			h.Close("test")

			Convey("Unsubscribed handlers from topic", func() {
				So(len(original.channels), ShouldBeZeroValue)
			})

		})

		Convey("Destroy", func() {
			handler := func() {}

			if h.Subscribe("test", handler) != nil {
				t.Fail()
			}

			if h.Subscribe("console", handler) != nil {
				t.Fail()
			}

			if h.Subscribe("console", func() {}) != nil {
				t.Fail()
			}

			So(h.Destroy(), ShouldBeNil)
			original := h.(*hub)
			So(len(original.channels), ShouldBeZeroValue)

		})

	})
}
