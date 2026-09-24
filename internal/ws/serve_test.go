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

func TestServeSubscriberCheckOrigin(t *testing.T) {
	hub := NewHub()
	render := func() ([]byte, error) { return []byte("render"), nil }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeSubscriber(w, r, hub, "game-1", render)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	host := strings.TrimPrefix(srv.URL, "http://")

	t.Run("matching origin succeeds", func(t *testing.T) {
		header := http.Header{"Origin": []string{"http://" + host}}
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err != nil {
			t.Fatalf("Dial() with matching Origin error: %v", err)
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("ReadMessage() error: %v", err)
		}
	})

	t.Run("mismatched origin is rejected", func(t *testing.T) {
		header := http.Header{"Origin": []string{"http://evil.example.com"}}
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err == nil {
			conn.Close()
			t.Fatal("Dial() with mismatched Origin succeeded, want an error")
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			t.Fatalf("Dial() with mismatched Origin status = %d, want %d", status, http.StatusForbidden)
		}
	})
}
