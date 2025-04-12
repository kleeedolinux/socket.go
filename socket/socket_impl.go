package socket

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/kleeedolinux/socket.go/socket/transport"
)


type socketImpl struct {
	id       string
	mu       sync.RWMutex
	handlers map[Event][]func(data interface{})

	transport transport.ServerTransport
	connected bool
}


func newSocketFromServerTransport(id string, t transport.ServerTransport) Socket {
	log.Printf("Creating new socket with ID: %s", id)
	s := &socketImpl{
		id:        id,
		handlers:  make(map[Event][]func(data interface{})),
		transport: t,
		connected: true,
	}

	
	go s.receiveLoop()

	return s
}


func (s *socketImpl) receiveLoop() {
	log.Printf("Socket %s: Starting receive loop", s.id)
	for {
		data, err := s.transport.Read()
		if err != nil {
			
			log.Printf("Socket %s: Read error: %v", s.id, err)
			s.triggerEvent(EventDisconnect, err)
			s.Close()
			return
		}

		log.Printf("Socket %s: Received raw data: %s", s.id, string(data))

		
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("Socket %s: Failed to unmarshal message: %v", s.id, err)
			s.triggerEvent(EventError, ErrInvalidMessage)
			continue
		}

		log.Printf("Socket %s: Received message event: %s", s.id, msg.Event)

		
		s.triggerEvent(msg.Event, msg.Data)
	}
}


func (s *socketImpl) ID() string {
	return s.id
}


func (s *socketImpl) Send(event Event, data interface{}) error {
	s.mu.RLock()
	connected := s.connected
	s.mu.RUnlock()

	if !connected {
		log.Printf("Socket %s: Attempted to send to closed socket", s.id)
		return ErrConnectionClosed
	}

	
	msg := Message{
		Event: event,
		Data:  data,
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Socket %s: Error marshaling message: %v", s.id, err)
		return err
	}

	log.Printf("Socket %s: Sending message: %s", s.id, string(jsonData))

	
	err = s.transport.Write(jsonData)
	if err != nil {
		log.Printf("Socket %s: Error writing to transport: %v", s.id, err)
	}
	return err
}


func (s *socketImpl) On(event Event, handler func(data interface{})) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Printf("Socket %s: Registering handler for event: %s", s.id, event)
	s.handlers[event] = append(s.handlers[event], handler)
}


func (s *socketImpl) Off(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Printf("Socket %s: Removing handlers for event: %s", s.id, event)
	delete(s.handlers, event)
}


func (s *socketImpl) triggerEvent(event Event, data interface{}) {
	s.mu.RLock()
	handlers := s.handlers[event]
	s.mu.RUnlock()

	log.Printf("Socket %s: Triggering event %s with %d handlers", s.id, event, len(handlers))

	for _, handler := range handlers {
		
		go handler(data)
	}
}


func (s *socketImpl) Close() error {
	s.mu.Lock()

	if !s.connected {
		s.mu.Unlock()
		return nil
	}

	log.Printf("Socket %s: Closing connection", s.id)
	s.connected = false
	s.mu.Unlock()

	s.triggerEvent(EventDisconnect, nil)

	return s.transport.Close()
}


func (s *socketImpl) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.connected
}
