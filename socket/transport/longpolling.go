package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)


type LongPollingTransport struct {
	mu            sync.Mutex
	client        *http.Client
	baseURL       string
	sessionID     string
	connected     bool
	incomingQueue chan []byte
	headers       http.Header

	
	ctx        context.Context
	cancelFunc context.CancelFunc

	
	pollInterval time.Duration
	timeout      time.Duration
}


type LongPollingOption func(*LongPollingTransport)


func WithLongPollingHeaders(headers http.Header) LongPollingOption {
	return func(t *LongPollingTransport) {
		for k, v := range headers {
			t.headers[k] = v
		}
	}
}


func WithPollInterval(interval time.Duration) LongPollingOption {
	return func(t *LongPollingTransport) {
		t.pollInterval = interval
	}
}


func WithTimeout(timeout time.Duration) LongPollingOption {
	return func(t *LongPollingTransport) {
		t.timeout = timeout
	}
}


func NewLongPollingTransport(baseURL string, opts ...LongPollingOption) *LongPollingTransport {
	ctx, cancel := context.WithCancel(context.Background())

	t := &LongPollingTransport{
		client:        &http.Client{},
		baseURL:       baseURL,
		headers:       make(http.Header),
		incomingQueue: make(chan []byte, 100),
		ctx:           ctx,
		cancelFunc:    cancel,
		pollInterval:  1 * time.Second,
		timeout:       30 * time.Second,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}


func (t *LongPollingTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connected {
		return nil
	}

	
	childCtx, cancel := context.WithCancel(ctx)
	t.ctx = childCtx
	t.cancelFunc = cancel

	
	req, err := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/connect", nil)
	if err != nil {
		return err
	}

	for k, values := range t.headers {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to connect: %s", resp.Status)
	}

	var connectResp struct {
		SessionID string `json:"sessionId"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&connectResp); err != nil {
		return err
	}

	t.sessionID = connectResp.SessionID
	t.connected = true

	
	go t.poll()

	return nil
}


func (t *LongPollingTransport) poll() {
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-time.After(t.pollInterval):
			msgs, err := t.fetchMessages()
			if err != nil {
				
				time.Sleep(t.pollInterval)
				continue
			}

			
			for _, msg := range msgs {
				select {
				case t.incomingQueue <- msg:
					
				default:
					
				}
			}
		}
	}
}


func (t *LongPollingTransport) fetchMessages() ([][]byte, error) {
	t.mu.Lock()
	sessionID := t.sessionID
	t.mu.Unlock()

	if sessionID == "" {
		return nil, errors.New("not connected")
	}

	ctx, cancel := context.WithTimeout(t.ctx, t.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/poll?sessionId=%s", t.baseURL, sessionID), nil)
	if err != nil {
		return nil, err
	}

	for k, values := range t.headers {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to poll: %s", resp.Status)
	}

	var messages []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return nil, err
	}

	result := make([][]byte, len(messages))
	for i, msg := range messages {
		result[i] = []byte(msg)
	}

	return result, nil
}


func (t *LongPollingTransport) Send(data []byte) error {
	t.mu.Lock()
	sessionID := t.sessionID
	t.mu.Unlock()

	if sessionID == "" {
		return errors.New("not connected")
	}

	ctx, cancel := context.WithTimeout(t.ctx, t.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/send?sessionId=%s", t.baseURL, sessionID),
		bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	for k, values := range t.headers {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to send message: %s - %s", resp.Status, string(bodyBytes))
	}

	return nil
}


func (t *LongPollingTransport) Receive() ([]byte, error) {
	t.mu.Lock()
	connected := t.connected
	t.mu.Unlock()

	if !connected {
		return nil, errors.New("not connected")
	}

	select {
	case <-t.ctx.Done():
		return nil, errors.New("connection closed")
	case msg := <-t.incomingQueue:
		return msg, nil
	}
}


func (t *LongPollingTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected {
		return nil
	}

	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/disconnect?sessionId=%s", t.baseURL, t.sessionID), nil)
	if err == nil {
		for k, values := range t.headers {
			for _, v := range values {
				req.Header.Add(k, v)
			}
		}

		resp, err := t.client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}

	
	t.cancelFunc()
	t.connected = false
	t.sessionID = ""

	return nil
}
