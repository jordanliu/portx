package daemon

import (
	"sync"

	"portx/internal/router"
)

const requestEventBuffer = 64

type requestBroker struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]requestSubscriber
}

func newRequestBroker() *requestBroker {
	return &requestBroker{subscribers: make(map[uint64]requestSubscriber)}
}

type requestSubscriber struct {
	routeID string
	events  chan router.RequestEvent
}

func (b *requestBroker) Subscribe(routeID string) (<-chan router.RequestEvent, func()) {
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	ch := make(chan router.RequestEvent, requestEventBuffer)
	b.subscribers[id] = requestSubscriber{routeID: routeID, events: ch}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, id)
			b.mu.Unlock()
		})
	}
}

func (b *requestBroker) Publish(event router.RequestEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subscriber := range b.subscribers {
		if subscriber.routeID != event.RouteID {
			continue
		}
		select {
		case subscriber.events <- event:
		default:
			// A request observer must never slow down the forwarding path.
		}
	}
}
