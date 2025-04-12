package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/kleeedolinux/socket.go/socket"
	"github.com/kleeedolinux/socket.go/socket/distributed"
)


type ChatMessage struct {
	Username string `json:"username"`
	Message  string `json:"message"`
	Room     string `json:"room"`
	Time     int64  `json:"time"`
}


type RoomState struct {
	mu       sync.RWMutex
	messages []ChatMessage
	users    map[string]bool
}


type ChatState struct {
	rooms map[string]*RoomState
	mu    sync.RWMutex
}

func newChatState() *ChatState {
	return &ChatState{
		rooms: make(map[string]*RoomState),
	}
}

func (s *ChatState) getOrCreateRoom(room string) *RoomState {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.rooms[room]; !exists {
		s.rooms[room] = &RoomState{
			messages: make([]ChatMessage, 0, 100),
			users:    make(map[string]bool),
		}
	}

	return s.rooms[room]
}

func (s *ChatState) getRoomNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.rooms))
	for name := range s.rooms {
		names = append(names, name)
	}

	return names
}


func roomManagerProcess(ctx context.Context, self distributed.ProcessID, node *distributed.Node, msg interface{}) error {
	data, ok := msg.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid message format")
	}

	msgType, ok := data["type"].(string)
	if !ok {
		return fmt.Errorf("missing message type")
	}

	server := node.SocketServer
	if server == nil {
		return fmt.Errorf("socket server not available")
	}

	switch msgType {
	case "room_join":
		room := data["room"].(string)
		socketID := data["socket"].(string)

		
		node.Subscribe(self, room)

		
		server.BroadcastToRoom(room, "system", map[string]interface{}{
			"message": "A user has joined the room",
			"room":    room,
			"count":   len(server.In(room)),
		})

		
		if state, ok := data["state"].(*ChatState); ok {
			roomState := state.getOrCreateRoom(room)
			roomState.mu.RLock()
			socket, exists := server.GetSocket(socketID)
			if exists {
				socket.Send("room_history", map[string]interface{}{
					"room":     room,
					"messages": roomState.messages,
				})
			}
			roomState.mu.RUnlock()
		}

	case "room_leave":
		room := data["room"].(string)

		
		server.BroadcastToRoom(room, "system", map[string]interface{}{
			"message": "A user has left the room",
			"room":    room,
			"count":   len(server.In(room)),
		})

	case "socket_disconnect":
		socketID := data["socket"].(string)

		
		server.LeaveAll(socketID)

		
		rooms := server.RoomsOf(socketID)
		for _, room := range rooms {
			server.BroadcastToRoom(room, "system", map[string]interface{}{
				"message": "A user has disconnected",
				"room":    room,
				"count":   len(server.In(room)),
			})
		}
	}

	return nil
}

func chatMessageProcess(ctx context.Context, self distributed.ProcessID, node *distributed.Node, msg interface{}) error {
	data, ok := msg.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid message format")
	}

	msgType, ok := data["type"].(string)
	if !ok {
		return fmt.Errorf("missing message type")
	}

	server := node.SocketServer
	if server == nil {
		return fmt.Errorf("socket server not available")
	}

	switch msgType {
	case "chat_message":
		chatMsg, ok := data["message"].(ChatMessage)
		if !ok {
			return fmt.Errorf("invalid chat message format")
		}

		
		if chatMsg.Time == 0 {
			chatMsg.Time = time.Now().Unix()
		}

		
		if state, ok := data["state"].(*ChatState); ok {
			
			roomState := state.getOrCreateRoom(chatMsg.Room)
			roomState.mu.Lock()
			roomState.messages = append(roomState.messages, chatMsg)
			
			if len(roomState.messages) > 100 {
				roomState.messages = roomState.messages[len(roomState.messages)-100:]
			}
			roomState.mu.Unlock()
		}

		
		server.BroadcastToRoom(chatMsg.Room, "chat", chatMsg)
	}

	return nil
}

func processMessage(node *distributed.Node, roomManager distributed.ProcessID, chatProcessor distributed.ProcessID, state *ChatState, s socket.Socket, event socket.Event, data interface{}) {
	switch event {
	case "join":
		room, ok := data.(string)
		if !ok {
			s.Send("error", "Invalid room name")
			return
		}

		
		if node.SocketServer != nil {
			node.SocketServer.Join(s.ID(), room)
		}

		
		node.Send(roomManager, map[string]interface{}{
			"type":   "room_join",
			"socket": s.ID(),
			"room":   room,
			"state":  state,
		})

	case "leave":
		room, ok := data.(string)
		if !ok {
			s.Send("error", "Invalid room name")
			return
		}

		
		if node.SocketServer != nil {
			node.SocketServer.Leave(s.ID(), room)
		}

		
		node.Send(roomManager, map[string]interface{}{
			"type":   "room_leave",
			"socket": s.ID(),
			"room":   room,
		})

	case "chat":
		var message ChatMessage

		
		if jsonData, err := json.Marshal(data); err == nil {
			if err := json.Unmarshal(jsonData, &message); err != nil {
				log.Printf("Error unmarshaling chat message: %v", err)
				return
			}
		} else {
			log.Printf("Error marshaling chat message: %v", err)
			return
		}

		
		node.Send(chatProcessor, map[string]interface{}{
			"type":    "chat_message",
			"message": message,
			"state":   state,
		})

	case "get_rooms":
		rooms := state.getRoomNames()
		s.Send("rooms", rooms)
	}
}

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	
	chatState := newChatState()

	
	server := socket.NewServer(
		socket.WithPingInterval(25*time.Second),
		socket.WithPingTimeout(5*time.Second),
		socket.WithMaxConcurrency(1000),
		socket.WithCompression(true),
		socket.WithBufferSize(4096),
	)

	
	node := distributed.NewNode("chat-node", distributed.WithSocketServer(server))

	
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

	
	_, err := distributed.NewSupervisor(supervisorSpec, node)
	if err != nil {
		log.Fatalf("Failed to create supervisor: %v", err)
	}

	
	roomManagerID := distributed.ProcessID("room-manager")
	chatProcessorID := distributed.ProcessID("chat-processor")

	
	server.HandleFunc(socket.EventConnect, func(s socket.Socket, _ interface{}) {
		log.Printf("Client connected: %s", s.ID())

		
		s.Send("system", map[string]interface{}{
			"message": fmt.Sprintf("Welcome! You are connected with ID: %s", s.ID()),
			"online":  server.Count(),
		})

		
		s.Send("rooms", chatState.getRoomNames())
	})

	server.HandleFunc(socket.EventDisconnect, func(s socket.Socket, _ interface{}) {
		log.Printf("Client disconnected: %s", s.ID())
	})

	
	for _, event := range []socket.Event{"join", "leave", "chat", "get_rooms"} {
		e := event 
		server.HandleFunc(e, func(s socket.Socket, data interface{}) {
			processMessage(node, roomManagerID, chatProcessorID, chatState, s, e, data)
		})
	}

	
	mux := http.NewServeMux()
	mux.HandleFunc("/socket", server.HandleHTTP)
	mux.HandleFunc("/socket/", server.HandleHTTP)
	mux.HandleFunc("/", serveIndexFile)

	
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: mux,
	}

	
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
		<-sigint

		log.Println("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		httpServer.Shutdown(ctx)
		node.Stop()
		os.Exit(0)
	}()

	log.Printf("Distributed chat server started on port %d", *port)
	log.Printf("Open http://localhost:%d in your browser", *port)

	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
}

func serveIndexFile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	
	indexPath := "./examples/distributed-chat/index.html"

	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		
		indexPath = "./index.html"
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			http.Error(w, "Could not find index.html", http.StatusInternalServerError)
			return
		}
	}

	http.ServeFile(w, r, indexPath)
}
