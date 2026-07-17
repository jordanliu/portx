package daemon

import (
	"testing"
	"time"

	"portx/internal/router"
)

func TestRequestBrokerPublishesWithoutBlocking(t *testing.T) {
	b := newRequestBroker()
	events, unsubscribe := b.Subscribe("route-1")
	defer unsubscribe()

	want := router.RequestEvent{RouteID: "route-1", Method: "GET", Path: "/health", Status: 200}
	b.Publish(want)

	select {
	case got := <-events:
		if got != want {
			t.Fatalf("event = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broker event")
	}
}

func TestRequestBrokerDropsEventsForSlowSubscriber(t *testing.T) {
	b := newRequestBroker()
	_, unsubscribe := b.Subscribe("route-1")
	defer unsubscribe()

	for i := 0; i < requestEventBuffer+10; i++ {
		b.Publish(router.RequestEvent{RouteID: "route-1", Method: "GET", Path: "/"})
	}
}

func TestRequestBrokerFiltersBeforeBuffering(t *testing.T) {
	b := newRequestBroker()
	events, unsubscribe := b.Subscribe("route-1")
	defer unsubscribe()

	for i := 0; i < requestEventBuffer+10; i++ {
		b.Publish(router.RequestEvent{RouteID: "route-2", Method: "GET", Path: "/noisy"})
	}
	want := router.RequestEvent{RouteID: "route-1", Method: "POST", Path: "/important"}
	b.Publish(want)

	select {
	case got := <-events:
		if got != want {
			t.Fatalf("event = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("matching event was dropped by unrelated traffic")
	}
}
