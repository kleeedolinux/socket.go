# Socket.go

A high-performance, concurrent WebSocket and long-polling communication library for Go applications, inspired by Socket.IO but optimized for maximum efficiency.

## Features

- **High-Performance**: Built for efficient concurrent message handling
- **Multiple Transports**: WebSocket and Long-polling supported
- **Room System**: Efficient room-based messaging for targeted communication
- **Message Batching**: Optimized message handling with batching support
- **Compression**: Optional WebSocket compression for bandwidth optimization
- **Reconnection**: Automatic reconnection with configurable backoff
- **Event-Based API**: Simple and familiar event-based programming model
- **Concurrent Message Processing**: Worker pools for efficient message distribution
- **Type-Safe**: Fully typed Go implementation with thread safety
- **Erlang-like Distributed System**: Robust process model with supervision trees and fault tolerance

## Installation

```bash
go get github.com/kleeedolinux/socket.go
```

## Quick Start

### Server

```go
package main

import (
	"log"
	"net/http"
	
	"github.com/kleeedolinux/socket.go/socket"
)

func main() {
	server := socket.NewServer()
	
	server.HandleFunc(socket.EventConnect, func(s socket.Socket, _ interface{}) {
		log.Printf("Client connected: %s", s.ID())
	})
	
	server.HandleFunc("message", func(s socket.Socket, data interface{}) {
		log.Printf("Received: %v", data)
		s.Send("reply", "Message received")
	})
	
	http.HandleFunc("/socket", server.HandleHTTP)
	http.ListenAndServe(":8080", nil)
}
```

### Client

```go
package main

import (
	"log"
	
	"github.com/kleeedolinux/socket.go/socket"
	"github.com/kleeedolinux/socket.go/socket/transport"
)

func main() {
	wsTransport := transport.NewWebSocketTransport("ws://localhost:8080/socket")
	client := socket.NewClient(wsTransport)
	
	client.On(socket.EventConnect, func(data interface{}) {
		log.Println("Connected!")
		client.Send("message", "Hello server")
	})
	
	client.On("reply", func(data interface{}) {
		log.Println("Server replied:", data)
	})
	
	client.Connect()
	
	// Keep program running
	select {}
}
```

## Documentation

For complete documentation and examples:

- [Basic Usage Guide](docs/howtouse.md) - Socket and room usage
- [Distributed System](docs/distributed.md) - Erlang-like processes and supervision

## Performance

Socket.go is designed with performance as a primary goal:

- **Efficient Concurrency**: Uses Go's concurrency primitives for maximum efficiency
- **Memory Optimization**: Careful memory management with buffer pooling
- **Minimal Overhead**: Lightweight design with minimal dependencies
- **Scalable Architecture**: Can handle thousands of concurrent connections
- **Robust Fault Tolerance**: Erlang-inspired supervision for self-healing systems

## Examples

- `examples/chat/` - Simple chat application
- `examples/distributed-chat/` - Distributed chat with process supervision

## License

MIT License 