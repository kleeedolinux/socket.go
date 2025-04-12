# Socket.go Distributed System

Socket.go includes a powerful Erlang-like distributed concurrency model that enables creating robust, fault-tolerant real-time applications. This system is inspired by Erlang/OTP's actor model and supervision strategies.

## Core Concepts

### Processes

Processes are lightweight concurrent units of execution that communicate exclusively through message passing. Unlike OS processes, they're extremely lightweight and can be created by the thousands.

```go
// Define a process handler function
func myProcess(ctx context.Context, self distributed.ProcessID, node *distributed.Node, msg interface{}) error {
    // Process messages here
    data, ok := msg.(map[string]interface{})
    if !ok {
        return fmt.Errorf("invalid message format")
    }
    
    // Do something with the message
    
    return nil
}

// Spawn a process
processID, err := node.SpawnProcess(myProcess)
```

### Nodes

A node is the container for processes and provides infrastructure for message passing, supervision, and room-based subscriptions.

```go
// Create a node and connect it to a socket server
node := distributed.NewNode("my-node", distributed.WithSocketServer(server))
```

### Supervision

Supervisors monitor child processes and can restart them using different strategies when they fail.

```go
// Create a supervisor specification
supervisorSpec := distributed.SupervisorSpec{
    ID:            "my-supervisor",
    Strategy:      distributed.OneForOne, // Restart only the failed process
    MaxRestarts:   5,                     // Maximum restarts within the period
    RestartPeriod: 60 * time.Second,      // Period for counting restarts
    Processes: []distributed.ProcessSpec{
        {ID: "process1", Function: process1Handler},
        {ID: "process2", Function: process2Handler},
    },
}

// Create the supervisor
supervisor, err := distributed.NewSupervisor(supervisorSpec, node)
```

### Room-Based Message Routing

Processes can subscribe to rooms, receiving automatic notifications when sockets join/leave or when events occur in those rooms.

```go
// Subscribe a process to events in a room
node.Subscribe(processID, "room-name")

// Unsubscribe a process from a room
node.Unsubscribe(processID, "room-name")
```

## Supervision Strategies

### OneForOne

When a process dies, only that process is restarted. This is the default strategy.

### OneForAll

When any process dies, all processes under the supervisor are restarted.

### RestForOne

When a process dies, that process and all processes defined after it are restarted.

## Message Passing

Messages are passed between processes using the `Send` method:

```go
// Send a message to a process
node.Send(targetProcessID, map[string]interface{}{
    "type": "command",
    "data": someData,
})
```

Each process has its own mailbox, and messages are processed one at a time in the order they are received.

## Real-World Example: Distributed Chat

Here's a simplified example of using the distributed system to create a chat application with room management:

```go
// Room manager process - handles room joins, leaves, and broadcasts room events
func roomManagerProcess(ctx context.Context, self distributed.ProcessID, node *distributed.Node, msg interface{}) error {
    data := msg.(map[string]interface{})
    msgType := data["type"].(string)
    
    switch msgType {
    case "room_join":
        room := data["room"].(string)
        socketID := data["socket"].(string)
        
        // Subscribe to room events
        node.Subscribe(self, room)
        
        // Notify room members about the new user
        node.SocketServer.BroadcastToRoom(room, "system", map[string]interface{}{
            "message": "A user has joined the room",
            "room":    room,
        })
        
    case "room_leave":
        room := data["room"].(string)
        
        // Notify room members
        node.SocketServer.BroadcastToRoom(room, "system", map[string]interface{}{
            "message": "A user has left the room",
            "room":    room,
        })
    }
    
    return nil
}

// Chat message process - handles persisting and broadcasting messages
func chatMessageProcess(ctx context.Context, self distributed.ProcessID, node *distributed.Node, msg interface{}) error {
    data := msg.(map[string]interface{})
    msgType := data["type"].(string)
    
    if msgType == "chat_message" {
        chatMsg := data["message"].(ChatMessage)
        room := chatMsg.Room
        
        // Save message to history (omitted for brevity)
        
        // Broadcast to room
        node.SocketServer.BroadcastToRoom(room, "chat", chatMsg)
    }
    
    return nil
}

func main() {
    // Create a socket server
    server := socket.NewServer()
    
    // Create a node for our distributed system
    node := distributed.NewNode("chat-node", distributed.WithSocketServer(server))
    
    // Create supervised processes
    supervisorSpec := distributed.SupervisorSpec{
        ID:            "chat-supervisor",
        Strategy:      distributed.OneForOne,
        MaxRestarts:   5,
        RestartPeriod: 60 * time.Second,
        Processes: []distributed.ProcessSpec{
            {ID: "room-manager", Function: roomManagerProcess},
            {ID: "chat-processor", Function: chatMessageProcess},
        },
    }
    
    distributed.NewSupervisor(supervisorSpec, node)
    
    // Socket event handlers
    server.HandleFunc("join", func(s socket.Socket, data interface{}) {
        room := data.(string)
        
        // Join the socket to the room
        server.Join(s.ID(), room)
        
        // Send message to room manager process
        node.Send("room-manager", map[string]interface{}{
            "type":   "room_join",
            "socket": s.ID(),
            "room":   room,
        })
    })
    
    server.HandleFunc("chat", func(s socket.Socket, data interface{}) {
        // Parse the message and send to chat processor
        node.Send("chat-processor", map[string]interface{}{
            "type":    "chat_message",
            "message": data,
        })
    })
    
    // Start the server
    http.HandleFunc("/socket", server.HandleHTTP)
    http.ListenAndServe(":8080", nil)
}
```

## Benefits

1. **Fault Isolation**: Errors in one process don't affect others
2. **Self-Healing**: Supervisors automatically restart failed processes
3. **Scalability**: Can create thousands of lightweight processes
4. **Modularity**: Each process handles a specific responsibility
5. **State Management**: Each process can maintain isolated state
6. **Automatic Event Propagation**: Room-based subscriptions automate event handling

## Advanced Usage

### Process Linking

Processes can be linked together, so when one fails, the other is notified or also terminates:

```go
// Link two processes
node.LinkProcesses(processA, processB)
```

### System Monitoring

You can monitor system-wide events:

```go
// Register a handler for node-wide monitoring
node.Monitor(func(event distributed.SystemEvent) {
    switch event.Type {
    case distributed.ProcessTerminated:
        log.Printf("Process %s terminated: %v", event.ProcessID, event.Error)
    }
})
```

## Complete Example

See `examples/distributed-chat/` for a complete implementation of a distributed chat application using this system.

## Performance Considerations

- Processes are lightweight but not free - for extremely high performance requirements, limit unnecessary process creation
- Message passing has some overhead compared to direct function calls
- The supervision tree adds robustness but introduces some complexity
