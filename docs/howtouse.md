# Socket.go Library

A high-performance, concurrent WebSocket and long-polling communication library for Go applications.

## Table of Contents

- [Features](#features)
- [Installation](#installation)
- [Server Usage](#server-usage)
  - [Basic Server](#basic-server)
  - [Server Options](#server-options)
  - [Event Handling](#event-handling)
  - [Room-Based Communication](#room-based-communication)
  - [Broadcasting Messages](#broadcasting-messages)
  - [Performance Optimization](#server-performance-optimization)
- [Client Usage](#client-usage)
  - [Basic Client](#basic-client)
  - [Client Options](#client-options)
  - [Event Handling](#client-event-handling)
  - [Room Management](#client-room-management)
  - [Transport Options](#transport-options)
  - [Performance Optimization](#client-performance-optimization)
- [Transport Types](#transport-types)
  - [WebSocket Transport](#websocket-transport)
  - [Long-Polling Transport](#long-polling-transport)
- [Error Handling](#error-handling)
- [Graceful Shutdown](#graceful-shutdown)
- [Advanced Usage](#advanced-usage)
  - [Custom Transports](#custom-transports)
  - [Message Batching](#message-batching)
  - [Compression](#compression)
  - [Secure Connections](#secure-connections)
- [Common Patterns](#common-patterns)
  - [Handling Reconnections](#handling-reconnections)
  - [Scaling Considerations](#scaling-considerations)
- [Distributed System](#distributed-system)

## Features

- **High-performance bidirectional communication**: Optimized for low latency and high throughput
- **Multiple transport options**: WebSocket (primary) and Long-polling (fallback)
- **Room-based messaging system**: Efficiently group and manage connections
- **Concurrent message handling**: Worker pools for optimal performance
- **Automatic reconnection**: Smart backoff strategies
- **Event-based API**: Simple, familiar programming model
- **Message batching**: Combine messages for better network efficiency
- **Compression support**: Reduce bandwidth usage
- **Thread-safe**: Designed for concurrent access
- **Distributed system**: Erlang-like process model with supervision

## Installation

```bash
go get github.com/kleeedolinux/socket.go
```

## Server Usage

### Basic Server

Creating a server is straightforward. The simplest setup requires just a few lines of code:

```go
package main

import (
	"log"
	"net/http"
	
	"github.com/kleeedolinux/socket.go/socket"
)

func main() {
	// Create a new server with default settings
	server := socket.NewServer()
	
	// Set up HTTP handler
	http.HandleFunc("/socket", server.HandleHTTP)
	
	// Start the server
	log.Println("Server starting on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("Server error:", err)
	}
}
```

This creates a server that can accept both WebSocket and long-polling connections at the `/socket` endpoint.

### Server Options

The server can be configured with various performance and behavior options:

```go
server := socket.NewServer(
	socket.WithMaxConcurrency(1000),         // Limit concurrent connections
	socket.WithCompression(true),            // Enable WebSocket compression
	socket.WithBufferSize(4096),             // Set buffer size for performance
	socket.WithPingInterval(20 * time.Second), // How often to ping clients
	socket.WithPingTimeout(5 * time.Second),   // How long to wait for pong
)
```

#### Option Details

| Option | Description | Default | Recommended |
|--------|-------------|---------|------------|
| `WithMaxConcurrency` | Maximum number of concurrent connections | 100 | Based on server resources |
| `WithCompression` | Enable WebSocket compression | false | true for text-heavy applications |
| `WithBufferSize` | Size of read/write buffers | 1024 | 4096 for most applications |
| `WithPingInterval` | Interval between ping messages | 25s | 20-30s for most applications |
| `WithPingTimeout` | Timeout for ping responses | 5s | 5-10s for most applications |

### Event Handling

The server uses an event-based model. You can register handlers for built-in events like connections and disconnections, or for custom events:

```go
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
	
	// Reply to this specific client
	s.Send("reply", "Message received")
})
```

#### Built-in Events

| Event | Description |
|-------|-------------|
| `socket.EventConnect` | Triggered when a client connects |
| `socket.EventDisconnect` | Triggered when a client disconnects |
| `socket.EventError` | Triggered when an error occurs |
| `socket.EventMessage` | Generic message event |

### Room-Based Communication

Rooms are a powerful way to organize connections and target messages to specific groups:

```go
// Add a socket to a room
server.Join(socketID, "room-name")

// Remove a socket from a room
server.Leave(socketID, "room-name")

// Remove a socket from all rooms
server.LeaveAll(socketID)

// Get all rooms a socket is in
rooms := server.RoomsOf(socketID)

// Get all sockets in a room
sockets := server.In("room-name")
```

A common pattern is to handle join/leave requests from clients:

```go
// Handle room join requests
server.HandleFunc("join", func(s socket.Socket, data interface{}) {
	room, ok := data.(string)
	if !ok {
		s.Send("error", "Invalid room name")
		return
	}
	
	// Add socket to room
	server.Join(s.ID(), room)
	
	// Notify room members
	server.BroadcastToRoom(room, "userJoined", map[string]interface{}{
		"userId": s.ID(),
		"room":   room,
	})
})

// Handle room leave requests
server.HandleFunc("leave", func(s socket.Socket, data interface{}) {
	room, ok := data.(string)
	if !ok {
		return
	}
	
	server.Leave(s.ID(), room)
})
```

### Broadcasting Messages

You can broadcast messages to all connected clients or to specific rooms:

```go
// Broadcast to all clients
server.Broadcast("announcement", "Server maintenance in 5 minutes")

// Broadcast to a specific room
server.BroadcastToRoom("room1", "notification", data)

// Broadcast to multiple rooms
server.BroadcastToRooms([]string{"room1", "room2"}, "notification", data)

// High-performance parallel broadcast (uses worker pool)
server.BroadcastParallel("event", data)
```

### Server Performance Optimization

For high-traffic servers, consider these optimizations:

1. **Limit Concurrency**: Prevent server overload
   ```go
   server := socket.NewServer(socket.WithMaxConcurrency(5000))
   ```

2. **Increase Buffer Sizes**: For larger messages
   ```go
   server := socket.NewServer(socket.WithBufferSize(8192))
   ```

3. **Enable Compression**: For bandwidth-heavy applications
   ```go
   server := socket.NewServer(socket.WithCompression(true))
   ```

4. **Use Worker Pools for Broadcasting**: For large fan-out scenarios
   ```go
   server.BroadcastParallel("event", data)
   ```

5. **Target Messages with Rooms**: Instead of broadcasting to all clients
   ```go
   server.BroadcastToRoom("room-name", "event", data)
   ```

## Client Usage

### Basic Client

Creating a client requires choosing a transport (usually WebSocket) and handling events:

```go
package main

import (
	"log"
	
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
	
	// Handle custom events
	client.On("reply", func(data interface{}) {
		log.Println("Server replied:", data)
	})
	
	// Connect to the server
	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	
	// Keep the program running
	select {}
}
```

### Client Options

The client can be configured with various options to control its behavior:

```go
client := socket.NewClient(
	transport,
	socket.WithReconnectDelay(1 * time.Second),        // Initial reconnect delay
	socket.WithMaxReconnectDelay(30 * time.Second),    // Maximum reconnect delay
	socket.WithReconnectAttempts(5),                   // Max reconnection attempts (0 = unlimited)
	socket.WithBatchSend(20, 100 * time.Millisecond),  // Enable message batching
	socket.WithClientCompression(true),                // Enable compression
)
```

#### Option Details

| Option | Description | Default | Recommended |
|--------|-------------|---------|------------|
| `WithReconnectDelay` | Initial delay before reconnecting | 1s | 1-2s |
| `WithMaxReconnectDelay` | Maximum reconnection delay | 30s | 30-60s |
| `WithReconnectAttempts` | Number of reconnection attempts | 5 | 0 (unlimited) for critical applications |
| `WithBatchSend` | Batch size and interval for messages | n/a | Enable for high-frequency messages |
| `WithClientCompression` | Enable compression | false | true for text-heavy applications |

### Client Event Handling

Register handlers for different events:

```go
// Built-in events
client.On(socket.EventConnect, func(data interface{}) {
	log.Println("Connected!")
})

client.On(socket.EventDisconnect, func(data interface{}) {
	log.Println("Disconnected:", data)
})

client.On(socket.EventError, func(data interface{}) {
	log.Println("Error:", data)
})

// Custom events
client.On("chat", func(data interface{}) {
	msg, ok := data.(map[string]interface{})
	if !ok {
		log.Println("Invalid message format")
		return
	}
	
	log.Printf("Chat: %s: %s", msg["user"], msg["text"])
})
```

Sending messages is straightforward:

```go
// Send a simple string message
client.Send("chat", "Hello world")

// Send a structured message
client.Send("chat", map[string]interface{}{
	"user": "Alice",
	"text": "Hello everyone!",
	"timestamp": time.Now().Unix(),
})
```

### Client Room Management

Clients can join and leave rooms:

```go
// Join a room
client.Join("room-name")

// Leave a room
client.Leave("room-name")

// Get rooms the client has joined
rooms := client.Rooms()

// Check if client is in a specific room
if client.InRoom("room-name") {
	// Do something
}
```

Joining a room typically requires server support:

```go
// Join a room (sends join event to server)
client.On(socket.EventConnect, func(data interface{}) {
	client.Send("join", "room-name")
})

// Handle room-specific messages
client.On("roomMessage", func(data interface{}) {
	msg := data.(map[string]interface{})
	log.Printf("Room message in %s: %s", msg["room"], msg["message"])
})
```

### Transport Options

#### WebSocket Transport

```go
wsTransport := transport.NewWebSocketTransport(
	"ws://localhost:8080/socket",
	transport.WithReadTimeout(30 * time.Second),
	transport.WithWriteTimeout(10 * time.Second),
	transport.WithCompression(true),
	transport.WithHeaders(map[string][]string{
		"Authorization": {"Bearer " + token},
	}),
)
```

#### Long-Polling Transport

```go
lpTransport := transport.NewLongPollingTransport(
	"http://localhost:8080/socket",
	transport.WithPollInterval(1 * time.Second),
	transport.WithTimeout(30 * time.Second),
	transport.WithHeaders(map[string][]string{
		"Authorization": {"Bearer " + token},
	}),
)
```

### Client Performance Optimization

For optimal client performance:

1. **Use WebSocket**: It's more efficient than Long-Polling
   ```go
   transport := transport.NewWebSocketTransport("ws://server/socket")
   ```

2. **Enable Message Batching**: For high-frequency messages
   ```go
   client := socket.NewClient(transport, socket.WithBatchSend(20, 50 * time.Millisecond))
   ```

3. **Enable Compression**: For bandwidth-constrained environments
   ```go
   transport := transport.NewWebSocketTransport("ws://server/socket", transport.WithCompression(true))
   client := socket.NewClient(transport, socket.WithClientCompression(true))
   ```

4. **Handle Reconnections Properly**: Restore state after reconnecting
   ```go
   client.On(socket.EventConnect, func(data interface{}) {
	   // Check if this is a reconnection
	   if client.ReconnectCount() > 0 {
		   // Restore state, rejoin rooms, etc.
	   }
   })
   ```

## Transport Types

### WebSocket Transport

WebSocket is the recommended transport for real-time applications. It provides:
- Full-duplex communication
- Low latency
- Low overhead
- Native browser support
- Compression options

```go
// Server side (automatic)
http.HandleFunc("/socket", server.HandleHTTP)

// Client side
wsTransport := transport.NewWebSocketTransport("ws://localhost:8080/socket")
client := socket.NewClient(wsTransport)
```

### Long-Polling Transport

Long-Polling is a fallback for environments where WebSockets are not available:
- Works behind restrictive proxies
- Compatible with older browsers
- Higher latency
- More overhead

```go
// Server side (automatic)
http.HandleFunc("/socket", server.HandleHTTP)

// Client side
lpTransport := transport.NewLongPollingTransport("http://localhost:8080/socket")
client := socket.NewClient(lpTransport)
```

## Error Handling

Proper error handling is essential for robust applications:

### Server-Side Error Handling

```go
// Handle global errors
server.HandleFunc(socket.EventError, func(s socket.Socket, data interface{}) {
	log.Printf("Error for client %s: %v", s.ID(), data)
})

// Handle errors in event handlers
server.HandleFunc("command", func(s socket.Socket, data interface{}) {
	cmd, ok := data.(map[string]interface{})
	if !ok {
		s.Send("error", "Invalid command format")
		return
	}
	
	// Process command
	result, err := processCommand(cmd)
	if err != nil {
		s.Send("error", map[string]interface{}{
			"code": "command_failed",
			"message": err.Error(),
		})
		return
	}
	
	s.Send("commandResult", result)
})
```

### Client-Side Error Handling

```go
// Handle connection errors
client.On(socket.EventError, func(data interface{}) {
	switch err := data.(type) {
	case *socket.ConnectionError:
		log.Printf("Connection error: %v", err)
		// Perhaps show a connection error UI
	case *socket.MessageError:
		log.Printf("Message error: %v", err)
		// Perhaps retry the message
	default:
		log.Printf("Unknown error: %v", err)
	}
})

// Handling send errors
if err := client.Send("message", data); err != nil {
	if errors.Is(err, socket.ErrConnectionClosed) {
		log.Println("Cannot send, connection closed")
	} else {
		log.Printf("Send error: %v", err)
	}
}
```

## Graceful Shutdown

### Server Graceful Shutdown

```go
// Create a shutdown context with timeout
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

// Shutdown the server
if err := server.Shutdown(ctx); err != nil {
	log.Printf("Error during shutdown: %v", err)
}

// The server will:
// 1. Stop accepting new connections
// 2. Send close messages to all connected clients
// 3. Wait for clients to disconnect or timeout
// 4. Close all underlying connections
```

### Client Graceful Disconnection

```go
// Send a notification before disconnecting (optional)
client.Send("goodbye", "Client disconnecting")

// Close the connection
if err := client.Close(); err != nil {
	log.Printf("Error closing connection: %v", err)
}

// The client will:
// 1. Send a WebSocket close frame if possible
// 2. Close the underlying connection
// 3. Trigger the disconnect event handler
```

## Advanced Usage

### Custom Transports

You can implement custom transports by satisfying the Transport interface:

```go
type Transport interface {
	Connect(ctx context.Context) error
	Send(data []byte) error
	Receive() ([]byte, error)
	Close() error
}

// Example custom transport
type MyCustomTransport struct {
	// Implementation details
}

func (t *MyCustomTransport) Connect(ctx context.Context) error {
	// Implementation
}

func (t *MyCustomTransport) Send(data []byte) error {
	// Implementation
}

func (t *MyCustomTransport) Receive() ([]byte, error) {
	// Implementation
}

func (t *MyCustomTransport) Close() error {
	// Implementation
}

// Use custom transport
client := socket.NewClient(&MyCustomTransport{})
```

### Message Batching

For high-frequency messages, batching improves performance by reducing network overhead:

```go
// Enable batching on client
client := socket.NewClient(
	transport,
	socket.WithBatchSend(20, 50 * time.Millisecond),
)

// Batching parameters:
// - 20: Maximum number of messages per batch
// - 50ms: Maximum delay before sending a batch
```

The batching system will:
1. Buffer messages up to the batch size
2. Send immediately when buffer is full
3. Send partial batches after the time limit
4. Handle serialization efficiently

### Compression

For text-heavy applications or bandwidth-constrained environments:

```go
// Server-side compression
server := socket.NewServer(socket.WithCompression(true))

// Transport-level compression
transport := transport.NewWebSocketTransport(
	"ws://localhost:8080/socket",
	transport.WithCompression(true),
)

// Client-side compression
client := socket.NewClient(
	transport,
	socket.WithClientCompression(true),
)
```

Compression works best for:
- Chat applications with large text messages
- JSON payloads with repetitive structures
- Mobile clients with limited bandwidth

### Secure Connections

For production applications, always use secure connections:

```go
// WebSocket with TLS (wss://)
transport := transport.NewWebSocketTransport("wss://example.com/socket")

// Long-polling with TLS (https://)
transport := transport.NewLongPollingTransport("https://example.com/socket")

// Custom TLS configuration
transport := transport.NewWebSocketTransport(
	"wss://example.com/socket",
	transport.WithTLSConfig(&tls.Config{
		// TLS configuration options
	}),
)
```

## Common Patterns

### Handling Reconnections

```go
client.On(socket.EventConnect, func(data interface{}) {
	reconnectCount := client.ReconnectCount()
	
	if reconnectCount > 0 {
		log.Printf("Reconnected after %d attempts", reconnectCount)
		
		// Rejoin rooms
		for _, room := range savedRooms {
			client.Join(room)
		}
		
		// Request state from server
		client.Send("getState", nil)
	} else {
		log.Println("Connected for the first time")
	}
})

client.On(socket.EventDisconnect, func(data interface{}) {
	log.Println("Disconnected, will attempt to reconnect...")
	
	// Save state that needs to be restored on reconnection
	savedRooms = client.Rooms()
})
```

### Scaling Considerations

For high-scale applications:

1. **Use room-based messaging**: More efficient than broadcasting to all clients
   ```go
   server.BroadcastToRoom("roomName", "event", data)
   ```

2. **Limit unnecessary events**: Be selective about what you broadcast
   ```go
   // Instead of broadcasting every change:
   server.BroadcastToRoom("room", "batchUpdate", batchedChanges)
   ```

3. **Implement proper error handling**: Prevent cascading failures
   ```go
   server.HandleFunc(socket.EventError, handleServerError)
   client.On(socket.EventError, handleClientError)
   ```

4. **Consider sharding**: For extremely large deployments
   ```go
   // Determine shard based on room name
   shardID := determineShardForRoom(roomName)
   shardServer := getShardServer(shardID)
   
   // Route message to appropriate shard
   shardServer.BroadcastToRoom(roomName, "event", data)
   ```

## Distributed System

For information about the Erlang-like distributed system, see [distributed.md](distributed.md).

## License

This library is licensed under the MIT License. 