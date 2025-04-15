package transport

import (
	"encoding/json"
	"io"

	"net/http"
	"sync"
	"time"

	"github.com/solviumdream/socket.go/debug"

	"github.com/gorilla/websocket"
)

type ServerTransport interface {
	Read() ([]byte, error)

	Write([]byte) error

	Close() error

	ID() string
}

type WebSocketServerTransport struct {
	id           string
	conn         *websocket.Conn
	sendCh       chan []byte
	closeCh      chan struct{}
	writeWg      sync.WaitGroup
	writeTimeout time.Duration
	mu           sync.Mutex
	closed       bool
}

type WebSocketServerConfig struct {
	WriteTimeout time.Duration
	ReadTimeout  time.Duration
	BufferSize   int
}

func DefaultWebSocketServerConfig() WebSocketServerConfig {
	return WebSocketServerConfig{
		WriteTimeout: 10 * time.Second,
		ReadTimeout:  30 * time.Second,
		BufferSize:   100,
	}
}

func NewWebSocketServerTransport(id string, conn *websocket.Conn, config WebSocketServerConfig) *WebSocketServerTransport {
	t := &WebSocketServerTransport{
		id:           id,
		conn:         conn,
		sendCh:       make(chan []byte, config.BufferSize),
		closeCh:      make(chan struct{}),
		writeTimeout: config.WriteTimeout,
	}

	if config.ReadTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(config.ReadTimeout))
	}

	t.writeWg.Add(1)
	go t.writePump()

	return t
}

func (t *WebSocketServerTransport) writePump() {
	defer t.writeWg.Done()

	for {
		select {
		case <-t.closeCh:
			return
		case message := <-t.sendCh:
			t.mu.Lock()
			if t.closed {
				t.mu.Unlock()
				return
			}

			if t.writeTimeout > 0 {
				t.conn.SetWriteDeadline(time.Now().Add(t.writeTimeout))
			}

			err := t.conn.WriteMessage(websocket.TextMessage, message)
			t.mu.Unlock()

			if err != nil {

				t.Close()
				return
			}
		}
	}
}

func (t *WebSocketServerTransport) Read() ([]byte, error) {
	debug.Printf("WebSocketServerTransport %s: Reading message", t.id)
	_, message, err := t.conn.ReadMessage()
	if err != nil {
		debug.Printf("WebSocketServerTransport %s: Error reading message: %v", t.id, err)
		t.Close()
		return nil, err
	}

	debug.Printf("WebSocketServerTransport %s: Received message: %s", t.id, string(message))
	return message, nil
}

func (t *WebSocketServerTransport) Write(data []byte) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()

	if closed {
		debug.Printf("WebSocketServerTransport %s: Attempted to write to closed transport", t.id)
		return nil
	}

	debug.Printf("WebSocketServerTransport %s: Sending message: %s", t.id, string(data))

	select {
	case t.sendCh <- data:
		return nil
	default:

		debug.Printf("WebSocketServerTransport %s: Send buffer full, closing connection", t.id)
		t.Close()
		return nil
	}
}

func (t *WebSocketServerTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}

	t.closed = true
	close(t.closeCh)
	t.mu.Unlock()

	t.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	)

	t.writeWg.Wait()
	return t.conn.Close()
}

func (t *WebSocketServerTransport) ID() string {
	return t.id
}

type LongPollingServerTransport struct {
	id                string
	pendingMessages   [][]byte
	incomingMessages  chan []byte
	mu                sync.Mutex
	lastActivity      time.Time
	closed            bool
	disconnectTimeout time.Duration
}

type LongPollingServerConfig struct {
	DisconnectTimeout time.Duration
	BufferSize        int
}

func DefaultLongPollingServerConfig() LongPollingServerConfig {
	return LongPollingServerConfig{
		DisconnectTimeout: 60 * time.Second,
		BufferSize:        100,
	}
}

func NewLongPollingServerTransport(id string, config LongPollingServerConfig) *LongPollingServerTransport {
	return &LongPollingServerTransport{
		id:                id,
		pendingMessages:   make([][]byte, 0),
		incomingMessages:  make(chan []byte, config.BufferSize),
		lastActivity:      time.Now(),
		disconnectTimeout: config.DisconnectTimeout,
	}
}

func (t *LongPollingServerTransport) Read() ([]byte, error) {
	select {
	case msg := <-t.incomingMessages:
		return msg, nil
	}
}

func (t *LongPollingServerTransport) Write(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	t.pendingMessages = append(t.pendingMessages, data)
	return nil
}

func (t *LongPollingServerTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.closed = true
	return nil
}

func (t *LongPollingServerTransport) ID() string {
	return t.id
}

func (t *LongPollingServerTransport) HandlePoll(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()

	if t.closed {
		t.mu.Unlock()
		http.Error(w, "Session closed", http.StatusGone)
		return
	}

	t.lastActivity = time.Now()

	messages := t.pendingMessages
	t.pendingMessages = make([][]byte, 0)

	t.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func (t *LongPollingServerTransport) HandleSend(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()

	if t.closed {
		t.mu.Unlock()
		http.Error(w, "Session closed", http.StatusGone)
		return
	}

	t.lastActivity = time.Now()
	t.mu.Unlock()

	defer r.Body.Close()

	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	select {
	case t.incomingMessages <- data:
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "Message queue full", http.StatusServiceUnavailable)
	}
}

func (t *LongPollingServerTransport) IsExpired() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return true
	}

	return time.Since(t.lastActivity) > t.disconnectTimeout
}

var Upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {

		return true
	},
}
