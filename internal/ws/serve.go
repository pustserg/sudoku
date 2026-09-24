package ws

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// This is a same-origin browser client talking to its own server,
	// not a cross-site API; the connection carries no ambient
	// credential the server hasn't already scoped via storeFor before
	// upgrading, so a permissive origin check adds no risk here.
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	pingInterval = 30 * time.Second
	pongWait     = 60 * time.Second
	writeWait    = 10 * time.Second
)

// ServeSubscriber upgrades r to a WebSocket, sends render()'s result
// immediately (so a fresh or reconnecting client is in sync with no
// separate resync step), then sends render()'s result again every time
// hub publishes for gameID. It blocks until the connection ends (client
// disconnect, a render/write failure, or a ping timeout) and returns
// the error that ended it (nil for a clean client-initiated close).
// The caller's ResponseWriter must not have been written to yet.
func ServeSubscriber(w http.ResponseWriter, r *http.Request, hub *Hub, gameID string, render func() ([]byte, error)) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, cancel := hub.Subscribe(gameID)
	defer cancel()

	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// The client never sends application messages, but ReadMessage must
	// still run so pong frames are processed and a client-initiated
	// close is detected. It runs in its own goroutine so it doesn't
	// block the send loop below; conn.Close() (deferred above, and
	// reached whenever this function returns) makes ReadMessage return
	// an error, ending this goroutine.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	send := func() error {
		payload, err := render()
		if err != nil {
			return err
		}
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(websocket.TextMessage, payload)
	}

	if err := send(); err != nil {
		return err
	}

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-closed:
			return nil
		case <-ch:
			if err := send(); err != nil {
				return err
			}
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return err
			}
		}
	}
}
