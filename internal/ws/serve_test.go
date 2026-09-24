package ws

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestServeSubscriberSendsInitialAndOnPublish(t *testing.T) {
	hub := NewHub()
	renderCount := 0
	render := func() ([]byte, error) {
		renderCount++
		return []byte(fmt.Sprintf("render-%d", renderCount)), nil
	}

	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done <- ServeSubscriber(w, r, hub, "game-1", render)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}
	defer conn.Close()

	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error: %v", err)
	}
	if string(msg) != "render-1" {
		t.Fatalf("initial message = %q, want %q", msg, "render-1")
	}

	hub.Publish("game-1")

	_, msg, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() after publish error: %v", err)
	}
	if string(msg) != "render-2" {
		t.Fatalf("message after publish = %q, want %q", msg, "render-2")
	}

	conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeSubscriber did not return after the client closed the connection")
	}
}
