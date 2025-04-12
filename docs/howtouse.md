# Socket.go Library

A high-performance, concurrent WebSocket and long-polling communication library for Go applications.

## Features

- High-performance bidirectional communication
- Multiple transport options (WebSocket, Long-polling)
- Room-based messaging system
- Efficient concurrent message handling
- Automatic reconnection
- Event-based API
- Message batching for optimal performance
- Compression support
- Fully thread-safe

## Installation

```bash
go get github.com/kleeedolinux/socket.go
```

## Server Usage

### Basic Server

```go
package main

import (
	"log"
	"net/http"
	
	"github.com/kleeedolinux/socket.go/socket"
)

func main() {
	// Create a new server
	server := socket.NewServer()
	
	// Handle connection events
	server.HandleFunc(socket.EventConnect, func(s socket.Socket, _ interface{}) {
		log.Printf("Client connected: %s", s.ID())
	})
	
	// Handle disconnect events
	server.HandleFunc(socket.EventDisconnect, func(s socket.Socket, _ interface{}) {
		log.Printf("Client disconnected: %s", s.ID())
	})
	
	// Handle custom messages
	server.HandleFunc("chat", func(s socket.Socket, data interface{}) {
		log.Printf("Received message from %s: %v", s.ID(), data)
		
		// Broadcast the message to all clients
		server.Broadcast("chat", data)
	})
	
	// Set up HTTP handler
	http.HandleFunc("/socket", server.HandleHTTP)
	
	// Start the server
	log.Println("Server starting on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("Server error:", err)
	}
}
```

### Server with Performance Options

```go
package main

import (
	"log"
	"net/http"
	"time"
	
	"github.com/kleeedolinux/socket.go/socket"
)

func main() {
	// Create a server with performance options
	server := socket.NewServer(
		socket.WithMaxConcurrency(1000),    // Limit concurrent connections
		socket.WithCompression(true),       // Enable WebSocket compression
		socket.WithBufferSize(4096),        // Set buffer size for performance
		socket.WithPingInterval(20 * time.Second),
		socket.WithPingTimeout(5 * time.Second),
	)
	
	// Handle connection/message events
	server.HandleFunc(socket.EventConnect, handleConnect)
	server.HandleFunc("message", handleMessage)
	
	// Set up HTTP handler
	http.HandleFunc("/socket", server.HandleHTTP)
	
	// Start the server
	log.Println("Server starting on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("Server error:", err)
	}
}

func handleConnect(s socket.Socket, _ interface{}) {
	log.Printf("Client connected: %s", s.ID())
}

func handleMessage(s socket.Socket, data interface{}) {
	log.Printf("Received message from %s: %v", s.ID(), data)
	s.Send("ack", "Message received")
}
```

### Room-Based Communication

```go
package main

import (
	"log"
	"net/http"
	
	"github.com/kleeedolinux/socket.go/socket"
)

func main() {
	server := socket.NewServer()
	
	// Handle connection events
	server.HandleFunc(socket.EventConnect, func(s socket.Socket, _ interface{}) {
		log.Printf("Client connected: %s", s.ID())
	})
	
	// Handle room join requests
	server.HandleFunc("join", func(s socket.Socket, data interface{}) {
		room, ok := data.(string)
		if !ok {
			s.Send("error", "Invalid room name")
			return
		}
		
		// Add socket to room
		server.Join(s.ID(), room)
		log.Printf("Client %s joined room: %s", s.ID(), room)
		
		// Notify room members
		server.BroadcastToRoom(room, "userJoined", map[string]string{
			"userId": s.ID(),
			"room":   room,
		})
	})
	
	// Handle room messages
	server.HandleFunc("roomMessage", func(s socket.Socket, data interface{}) {
		msg, ok := data.(map[string]interface{})
		if !ok {
			return
		}
		
		roomName, ok := msg["room"].(string)
		if !ok {
			return
		}
		
		// Send message to the room
		server.BroadcastToRoom(roomName, "roomMessage", map[string]interface{}{
			"sender":  s.ID(),
			"room":    roomName,
			"message": msg["message"],
		})
	})
	
	// Handle room leave requests
	server.HandleFunc("leave", func(s socket.Socket, data interface{}) {
		room, ok := data.(string)
		if !ok {
			return
		}
		
		server.Leave(s.ID(), room)
		log.Printf("Client %s left room: %s", s.ID(), room)
	})
	
	http.HandleFunc("/socket", server.HandleHTTP)
	log.Println("Server starting on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("Server error:", err)
	}
}
```

### High-Performance Broadcasting

```go
// Broadcast to all clients with worker pool for efficiency
server.BroadcastParallel("announcement", "Server maintenance in 5 minutes")

// Broadcast to specific rooms
server.BroadcastToRooms([]string{"room1", "room2"}, "notification", data)

// Broadcast to a single room
server.BroadcastToRoom("gameRoom", "gameState", gameState)
```

## Client Usage

### Basic WebSocket Client

```go
package main

import (
	"context"
	"log"
	"time"
	
	"github.com/kleeedolinux/socket.go/socket"
	"github.com/kleeedolinux/socket.go/socket/transport"
)

func main() {
	// Create WebSocket transport
	wsTransport := transport.NewWebSocketTransport("ws://localhost:8080/socket")
	
	// Create client with transport
	client := socket.NewClient(wsTransport)
	
	// Handle connect event
	client.On(socket.EventConnect, func(data interface{}) {
		log.Println("Connected to server!")
		
		// Send a message after connecting
		client.Send("chat", "Hello, server!")
	})
	
	// Handle disconnect
	client.On(socket.EventDisconnect, func(data interface{}) {
		log.Println("Disconnected from server")
	})
	
	// Handle error
	client.On(socket.EventError, func(data interface{}) {
		log.Println("Error:", data)
	})
	
	// Handle custom messages
	client.On("chat", func(data interface{}) {
		log.Println("Chat message:", data)
	})
	
	// Connect to the server
	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	
	// Keep the program running
	time.Sleep(60 * time.Second)
	
	// Close the connection when done
	client.Close()
}
```

### Client with Advanced Options

```go
package main

import (
	"log"
	"time"
	
	"github.com/kleeedolinux/socket.go/socket"
	"github.com/kleeedolinux/socket.go/socket/transport"
)

func main() {
	// Configure WebSocket transport
	wsTransport := transport.NewWebSocketTransport(
		"ws://localhost:8080/socket",
		transport.WithReadTimeout(30 * time.Second),
		transport.WithWriteTimeout(10 * time.Second),
		transport.WithCompression(true),
		transport.WithHeaders(map[string][]string{
			"X-Custom-Header": {"value"},
		}),
	)
	
	// Create client with advanced options
	client := socket.NewClient(
		wsTransport,
		socket.WithReconnectDelay(1 * time.Second),
		socket.WithMaxReconnectDelay(30 * time.Second),
		socket.WithReconnectAttempts(5),
		socket.WithBatchSend(20, 100 * time.Millisecond),
		socket.WithClientCompression(true),
	)
	
	// Set up event handlers
	client.On(socket.EventConnect, handleConnect)
	client.On(socket.EventDisconnect, handleDisconnect)
	client.On(socket.EventError, handleError)
	client.On("message", handleMessage)
	
	// Connect to server
	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	
	// Wait for events
	select {}
}

func handleConnect(data interface{}) {
	log.Println("Connected to server")
}

func handleDisconnect(data interface{}) {
	log.Println("Disconnected from server")
}

func handleError(data interface{}) {
	log.Println("Error:", data)
}

func handleMessage(data interface{}) {
	log.Println("Received message:", data)
}
```

### Room Management with Client

```go
package main

import (
	"log"
	"time"
	
	"github.com/kleeedolinux/socket.go/socket"
	"github.com/kleeedolinux/socket.go/socket/transport"
)

func main() {
	wsTransport := transport.NewWebSocketTransport("ws://localhost:8080/socket")
	client := socket.NewClient(wsTransport)
	
	// Connect to server
	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	
	// Handle connection event
	client.On(socket.EventConnect, func(data interface{}) {
		log.Println("Connected to server")
		
		// Join a room
		client.Join("chatRoom")
	})
	
	// Handle room messages
	client.On("roomMessage", func(data interface{}) {
		msg, ok := data.(map[string]interface{})
		if !ok {
			return
		}
		
		log.Printf("Room message from %s in %s: %v", 
			msg["sender"], msg["room"], msg["message"])
	})
	
	// Handle user joined notifications
	client.On("userJoined", func(data interface{}) {
		info, ok := data.(map[string]interface{})
		if !ok {
			return
		}
		
		log.Printf("User %s joined room %s", info["userId"], info["room"])
	})
	
	// Send message to room after 2 seconds
	time.AfterFunc(2*time.Second, func() {
		client.Send("roomMessage", map[string]interface{}{
			"room":    "chatRoom",
			"message": "Hello everyone!",
		})
	})
	
	// Leave room after 10 seconds
	time.AfterFunc(10*time.Second, func() {
		client.Leave("chatRoom")
	})
	
	// Keep program running
	select {}
}
```

## Long-Polling Transport

### Server Side

The same server code works for both WebSocket and Long-Polling transports automatically.

### Client Side

```go
package main

import (
	"log"
	
	"github.com/kleeedolinux/socket.go/socket"
	"github.com/kleeedolinux/socket.go/socket/transport"
)

func main() {
	// Create a Long-Polling transport
	lpTransport := transport.NewLongPollingTransport(
		"http://localhost:8080/socket",
		transport.WithPollInterval(1 * time.Second),
		transport.WithTimeout(30 * time.Second),
	)
	
	// Create client with transport
	client := socket.NewClient(lpTransport)
	
	// Set up event handlers
	client.On(socket.EventConnect, func(data interface{}) {
		log.Println("Connected to server")
	})
	
	client.On("message", func(data interface{}) {
		log.Println("Received message:", data)
	})
	
	// Connect to server
	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	
	// Send a message
	client.Send("message", "Hello from long-polling client")
	
	// Keep program running
	select {}
}
```

## Performance Optimization Tips

1. **Use WebSocket Transport when possible**: WebSocket is more efficient than Long-Polling for real-time communication.

2. **Enable Message Batching**: For high-frequency messages, use batch sending to reduce overhead:
   ```go
   client := socket.NewClient(transport, socket.WithBatchSend(20, 50 * time.Millisecond))
   ```

3. **Use Room-Based Communication**: Instead of broadcasting to all clients, use rooms to target specific groups.

4. **Enable Compression**: For large payloads or bandwidth-constrained environments:
   ```go
   // Server
   server := socket.NewServer(socket.WithCompression(true))
   
   // Client
   transport := transport.NewWebSocketTransport(url, transport.WithCompression(true))
   client := socket.NewClient(transport, socket.WithClientCompression(true))
   ```

5. **Set Appropriate Buffer Sizes**: Tune buffer sizes based on your message patterns:
   ```go
   server := socket.NewServer(socket.WithBufferSize(8192))
   ```

6. **Limit Concurrency**: For high-traffic servers, set a maximum connection limit:
   ```go
   server := socket.NewServer(socket.WithMaxConcurrency(5000))
   ```

7. **Use Parallel Broadcasting**: For large broadcasts, use the parallel version:
   ```go
   server.BroadcastParallel("event", data) // Uses worker pool
   ```

## Error Handling

Always handle error events on both client and server:

```go
// Client-side
client.On(socket.EventError, func(data interface{}) {
    log.Printf("Error: %v", data)
})

// Server-side
server.HandleFunc(socket.EventError, func(s socket.Socket, data interface{}) {
    log.Printf("Error for client %s: %v", s.ID(), data)
})
```

## Graceful Shutdown

```go
// Server-side graceful shutdown
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
server.Shutdown(ctx)

// Client-side graceful disconnection
client.Close()
```

## Custom Transports

You can implement custom transports by satisfying the Transport interface (client) or ServerTransport interface (server).

```go
type Transport interface {
    Connect(ctx context.Context) error
    Send(data []byte) error
    Receive() ([]byte, error)
    Close() error
}
```

## License

This library is licensed under the MIT License. 