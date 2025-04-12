package socket

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

type Client struct {
	mu       sync.RWMutex
	id       string
	conn     Transport
	handlers map[Event][]func(data interface{})

	rooms map[string]bool

	connected bool
	sendCh    chan Message

	batchSize       int
	batchInterval   time.Duration
	messageBufferCh chan Message

	compressionEnabled bool

	reconnectDelay    time.Duration
	maxReconnectDelay time.Duration
	reconnectAttempts int

	ctx        context.Context
	cancelFunc context.CancelFunc
}

type Transport interface {
	Connect(ctx context.Context) error
	Send(data []byte) error
	Receive() ([]byte, error)
	Close() error
}

type ClientOption func(*Client)

func WithReconnectDelay(d time.Duration) ClientOption {
	return func(c *Client) {
		c.reconnectDelay = d
	}
}

func WithMaxReconnectDelay(d time.Duration) ClientOption {
	return func(c *Client) {
		c.maxReconnectDelay = d
	}
}

func WithReconnectAttempts(attempts int) ClientOption {
	return func(c *Client) {
		c.reconnectAttempts = attempts
	}
}

func WithBatchSend(batchSize int, interval time.Duration) ClientOption {
	return func(c *Client) {
		c.batchSize = batchSize
		c.batchInterval = interval
	}
}

func WithClientCompression(enabled bool) ClientOption {
	return func(c *Client) {
		c.compressionEnabled = enabled
	}
}

func NewClient(transport Transport, opts ...ClientOption) *Client {
	ctx, cancel := context.WithCancel(context.Background())

	client := &Client{
		id:                 generateID(),
		conn:               transport,
		handlers:           make(map[Event][]func(data interface{})),
		rooms:              make(map[string]bool),
		sendCh:             make(chan Message, 100),
		messageBufferCh:    make(chan Message, 1000),
		batchSize:          10,
		batchInterval:      50 * time.Millisecond,
		compressionEnabled: false,
		reconnectDelay:     1 * time.Second,
		maxReconnectDelay:  30 * time.Second,
		reconnectAttempts:  5,
		ctx:                ctx,
		cancelFunc:         cancel,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

func (c *Client) ID() string {
	return c.id
}

func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	roomsToRejoin := make([]string, 0, len(c.rooms))
	for room := range c.rooms {
		roomsToRejoin = append(roomsToRejoin, room)
	}

	if err := c.conn.Connect(c.ctx); err != nil {
		c.mu.Unlock()
		return err
	}

	c.connected = true
	c.mu.Unlock()

	go c.sendLoop()
	go c.receiveLoop()
	go c.batchProcessingLoop()

	for _, room := range roomsToRejoin {
		c.Send("join", room)
	}

	return nil
}

func (c *Client) sendLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case msg := <-c.sendCh:
			data, err := json.Marshal(msg)
			if err != nil {
				c.triggerEvent(EventError, err)
				continue
			}

			if err := c.conn.Send(data); err != nil {
				c.triggerEvent(EventError, err)
				c.handleDisconnect(err)
				return
			}
		}
	}
}

func (c *Client) receiveLoop() {
	for {
		data, err := c.conn.Receive()
		if err != nil {
			c.triggerEvent(EventError, err)
			c.handleDisconnect(err)
			return
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			c.triggerEvent(EventError, ErrInvalidMessage)
			continue
		}

		c.triggerEvent(msg.Event, msg.Data)
	}
}

func (c *Client) handleDisconnect(err error) {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return
	}

	c.connected = false
	c.mu.Unlock()

	c.triggerEvent(EventDisconnect, err)

	if c.reconnectAttempts != 0 {
		go c.reconnect()
	}
}

func (c *Client) reconnect() {
	delay := c.reconnectDelay
	attempts := 0

	for c.reconnectAttempts <= 0 || attempts < c.reconnectAttempts {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(delay):

			if err := c.Connect(); err == nil {
				return
			}

			attempts++

			delay *= 2
			if delay > c.maxReconnectDelay {
				delay = c.maxReconnectDelay
			}
		}
	}
}

func (c *Client) Send(event Event, data interface{}) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return ErrConnectionClosed
	}

	msg := Message{Event: event, Data: data}

	if c.batchSize > 1 {
		select {
		case c.messageBufferCh <- msg:
			return nil
		default:
			return errors.New("message buffer full")
		}
	} else {
		select {
		case c.sendCh <- msg:
			return nil
		default:
			return errors.New("send buffer full")
		}
	}
}

func (c *Client) On(event Event, handler func(data interface{})) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.handlers[event] = append(c.handlers[event], handler)
}

func (c *Client) Off(event Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.handlers, event)
}

func (c *Client) triggerEvent(event Event, data interface{}) {
	c.mu.RLock()
	handlers := c.handlers[event]
	c.mu.RUnlock()

	for _, handler := range handlers {
		go handler(data)
	}
}

func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.connected
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.cancelFunc()
	c.connected = false

	return c.conn.Close()
}

func (c *Client) batchProcessingLoop() {
	if c.batchSize <= 1 {
		return
	}

	batch := make([]Message, 0, c.batchSize)
	ticker := time.NewTicker(c.batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case msg := <-c.messageBufferCh:
			batch = append(batch, msg)

			if len(batch) >= c.batchSize {
				c.processBatch(batch)
				batch = make([]Message, 0, c.batchSize)
			}
		case <-ticker.C:
			if len(batch) > 0 {
				c.processBatch(batch)
				batch = make([]Message, 0, c.batchSize)
			}
		}
	}
}

func (c *Client) processBatch(messages []Message) {
	if len(messages) == 0 {
		return
	}

	jsonMessages := make([][]byte, 0, len(messages))

	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			c.triggerEvent(EventError, err)
			continue
		}
		jsonMessages = append(jsonMessages, data)
	}

	if batchTransport, ok := c.conn.(BatchTransport); ok && len(jsonMessages) > 0 {
		if err := batchTransport.BatchSend(jsonMessages); err != nil {
			c.triggerEvent(EventError, err)
			c.handleDisconnect(err)
		}
	} else {
		for _, msg := range jsonMessages {
			if err := c.conn.Send(msg); err != nil {
				c.triggerEvent(EventError, err)
				c.handleDisconnect(err)
				return
			}
		}
	}
}

func (c *Client) Join(room string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.rooms[room] = true

	if c.connected {
		c.Send("join", room)
	}
}

func (c *Client) Leave(room string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.rooms, room)

	if c.connected {
		c.Send("leave", room)
	}
}

func (c *Client) Rooms() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rooms := make([]string, 0, len(c.rooms))
	for room := range c.rooms {
		rooms = append(rooms, room)
	}

	return rooms
}

func (c *Client) InRoom(room string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, exists := c.rooms[room]
	return exists
}

func (c *Client) setupTransport(t Transport) {

	if c.compressionEnabled {
		if compressibleTransport, ok := t.(CompressibleTransport); ok {
			compressibleTransport.EnableCompression(true)
		}
	}
}

type CompressibleTransport interface {
	Transport
	EnableCompression(bool)
}

type BatchTransport interface {
	Transport
	BatchSend([][]byte) error
}
