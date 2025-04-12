


package socket

import (
	"errors"
)


type Event string


const (
	EventConnect    Event = "connect"
	EventDisconnect Event = "disconnect"
	EventError      Event = "error"
	EventMessage    Event = "message"
)


type Message struct {
	Event Event       `json:"event"`
	Data  interface{} `json:"data,omitempty"`
}


type Socket interface {
	
	ID() string

	
	Send(event Event, data interface{}) error

	
	On(event Event, handler func(data interface{}))

	
	Off(event Event)

	
	Close() error

	
	IsConnected() bool
}


var (
	ErrConnectionClosed = errors.New("connection closed")
	ErrInvalidMessage   = errors.New("invalid message format")
	ErrTimeout          = errors.New("operation timed out")
)
