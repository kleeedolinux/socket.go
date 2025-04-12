package transport

import (
	"context"
	"errors"

	"net/http"
	"sync"
	"time"

	"github.com/kleeedolinux/socket.go/debug"

	"github.com/gorilla/websocket"
)

type WebSocketTransport struct {
	mu           sync.Mutex
	conn         *websocket.Conn
	url          string
	dialer       *websocket.Dialer
	headers      http.Header
	connected    bool
	readTimeout  time.Duration
	writeTimeout time.Duration
	compression  bool
}

type WebSocketOption func(*WebSocketTransport)

func WithHeaders(headers http.Header) WebSocketOption {
	return func(t *WebSocketTransport) {
		t.headers = headers
	}
}

func WithReadTimeout(timeout time.Duration) WebSocketOption {
	return func(t *WebSocketTransport) {
		t.readTimeout = timeout
	}
}

func WithWriteTimeout(timeout time.Duration) WebSocketOption {
	return func(t *WebSocketTransport) {
		t.writeTimeout = timeout
	}
}

func WithCompression(enabled bool) WebSocketOption {
	return func(t *WebSocketTransport) {
		t.compression = enabled
	}
}

func NewWebSocketTransport(url string, opts ...WebSocketOption) *WebSocketTransport {
	t := &WebSocketTransport{
		url:          url,
		dialer:       websocket.DefaultDialer,
		headers:      make(http.Header),
		readTimeout:  30 * time.Second,
		writeTimeout: 10 * time.Second,
		compression:  false,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

func (t *WebSocketTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connected {
		return nil
	}

	debug.Printf("WebSocketTransport: Connecting to %s", t.url)

	dialer := *t.dialer
	dialer.HandshakeTimeout = 10 * time.Second

	if t.compression {
		dialer.EnableCompression = true
	}

	conn, _, err := dialer.DialContext(ctx, t.url, t.headers)
	if err != nil {
		debug.Printf("WebSocketTransport: Connection failed: %v", err)
		return err
	}

	debug.Printf("WebSocketTransport: Connected successfully")
	t.conn = conn
	t.connected = true

	return nil
}

func (t *WebSocketTransport) Send(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected || t.conn == nil {
		return errors.New("not connected")
	}

	if t.writeTimeout > 0 {
		if err := t.conn.SetWriteDeadline(time.Now().Add(t.writeTimeout)); err != nil {
			debug.Printf("WebSocketTransport: Error setting write deadline: %v", err)
			return err
		}
	}

	debug.Printf("WebSocketTransport: Sending data: %s", string(data))
	err := t.conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		debug.Printf("WebSocketTransport: Send error: %v", err)
	}
	return err
}

func (t *WebSocketTransport) Receive() ([]byte, error) {
	t.mu.Lock()
	if !t.connected || t.conn == nil {
		t.mu.Unlock()
		return nil, errors.New("not connected")
	}

	if t.readTimeout > 0 {
		if err := t.conn.SetReadDeadline(time.Now().Add(t.readTimeout)); err != nil {
			t.mu.Unlock()
			debug.Printf("WebSocketTransport: Error setting read deadline: %v", err)
			return nil, err
		}
	}
	t.mu.Unlock()

	_, message, err := t.conn.ReadMessage()
	if err != nil {
		debug.Printf("WebSocketTransport: Read error: %v", err)
		return nil, err
	}

	debug.Printf("WebSocketTransport: Received data: %s", string(message))
	return message, nil
}

func (t *WebSocketTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected || t.conn == nil {
		return nil
	}

	debug.Printf("WebSocketTransport: Closing connection")

	err := t.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	)
	if err != nil {
		debug.Printf("WebSocketTransport: Error sending close message: %v", err)

	}

	err = t.conn.Close()
	if err != nil {
		debug.Printf("WebSocketTransport: Error closing connection: %v", err)
	}

	t.connected = false
	t.conn = nil

	return err
}

func (t *WebSocketTransport) ReceiveConcurrent(numWorkers int, handler func([]byte, error)) {
	t.mu.Lock()
	if !t.connected || t.conn == nil {
		t.mu.Unlock()
		handler(nil, errors.New("not connected"))
		return
	}
	t.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	messages := make(chan []byte, numWorkers)
	errs := make(chan error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			for msg := range messages {
				handler(msg, nil)
			}
		}()
	}

	go func() {
		for err := range errs {
			handler(nil, err)
		}
	}()

	go func() {
		defer close(messages)
		defer close(errs)

		for {
			t.mu.Lock()
			if !t.connected || t.conn == nil {
				t.mu.Unlock()
				errs <- errors.New("connection closed")
				return
			}

			if t.readTimeout > 0 {
				if err := t.conn.SetReadDeadline(time.Now().Add(t.readTimeout)); err != nil {
					t.mu.Unlock()
					errs <- err
					return
				}
			}
			t.mu.Unlock()

			_, message, err := t.conn.ReadMessage()
			if err != nil {
				errs <- err
				return
			}

			messages <- message
		}
	}()

	wg.Wait()
}

func (t *WebSocketTransport) ReceiveWithContext(ctx context.Context, handler func([]byte, error)) {
	t.mu.Lock()
	if !t.connected || t.conn == nil {
		t.mu.Unlock()
		handler(nil, errors.New("not connected"))
		return
	}
	t.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				handler(nil, ctx.Err())
				return
			default:
				t.mu.Lock()
				if !t.connected || t.conn == nil {
					t.mu.Unlock()
					handler(nil, errors.New("connection closed"))
					return
				}

				if t.readTimeout > 0 {
					if err := t.conn.SetReadDeadline(time.Now().Add(t.readTimeout)); err != nil {
						t.mu.Unlock()
						handler(nil, err)
						return
					}
				}
				t.mu.Unlock()

				_, message, err := t.conn.ReadMessage()
				if err != nil {
					handler(nil, err)
					return
				}

				handler(message, nil)
			}
		}
	}()
}

func (t *WebSocketTransport) BatchSend(messages [][]byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected || t.conn == nil {
		return errors.New("not connected")
	}

	if t.writeTimeout > 0 {
		if err := t.conn.SetWriteDeadline(time.Now().Add(t.writeTimeout)); err != nil {
			return err
		}
	}

	for _, msg := range messages {
		if err := t.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return err
		}
	}

	return nil
}

func (t *WebSocketTransport) EnableCompression(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.compression = enabled

	if t.connected && t.conn != nil {
		if enabler, ok := t.conn.UnderlyingConn().(interface{ EnableWriteCompression(bool) }); ok {
			enabler.EnableWriteCompression(enabled)
		}
	}
}
