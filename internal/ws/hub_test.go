package ws

import (
	"sync"
	"testing"
	"time"
)

func TestPublishDeliversToSubscriber(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("game-1")
	defer cancel()

	h.Publish("game-1")

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("did not receive notification after Publish")
	}
}

func TestPublishToUnknownGameIsNoop(t *testing.T) {
	h := NewHub()
	h.Publish("no-such-game") // must not panic
}

func TestPublishDoesNotReachOtherGames(t *testing.T) {
	h := NewHub()
	chA, cancelA := h.Subscribe("game-a")
	defer cancelA()
	chB, cancelB := h.Subscribe("game-b")
	defer cancelB()

	h.Publish("game-a")

	select {
	case <-chA:
	case <-time.After(time.Second):
		t.Fatal("game-a subscriber did not receive notification")
	}
	select {
	case <-chB:
		t.Fatal("game-b subscriber received a notification meant for game-a")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCancelStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("game-1")
	cancel()

	h.Publish("game-1") // must not panic or block, even though ch is now unsubscribed

	select {
	case <-ch:
		t.Fatal("received a notification after cancel")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestConcurrentSubscribePublishCancel(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := h.Subscribe("game-1")
			defer cancel()
			h.Publish("game-1")
			select {
			case <-ch:
			case <-time.After(time.Second):
			}
		}()
	}
	h.Publish("game-1")
	wg.Wait()
}
