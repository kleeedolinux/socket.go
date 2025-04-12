package socket

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/kleeedolinux/socket.go/socket/transport"
)

type Server struct {
	mu       sync.RWMutex
	sockets  map[string]Socket
	handlers map[Event][]func(s Socket, data interface{})
	sessions map[string]*LongPollingSession

	roomManager *RoomManager

	pingInterval         time.Duration
	pingTimeout          time.Duration
	maxConcurrency       int
	concurrencySemaphore chan struct{}
	compressionEnabled   bool
	bufferSize           int
}

type LongPollingSession struct {
	ID         string
	Transport  *transport.LongPollingServerTransport
	SocketImpl *socketImpl
}

func NewServer(opts ...ServerOption) *Server {
	s := &Server{
		sockets:            make(map[string]Socket),
		handlers:           make(map[Event][]func(s Socket, data interface{})),
		sessions:           make(map[string]*LongPollingSession),
		roomManager:        NewRoomManager(),
		pingInterval:       25 * time.Second,
		pingTimeout:        5 * time.Second,
		maxConcurrency:     100,
		bufferSize:         1024,
		compressionEnabled: false,
	}

	for _, opt := range opts {
		opt(s)
	}

	if s.concurrencySemaphore == nil && s.maxConcurrency > 0 {
		s.concurrencySemaphore = make(chan struct{}, s.maxConcurrency)
	}

	go s.cleanupSessions()

	return s
}

func (s *Server) cleanupSessions() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		for id, session := range s.sessions {
			if session.Transport.IsExpired() {
				delete(s.sessions, id)
				delete(s.sockets, session.SocketImpl.id)
				session.SocketImpl.Close()
			}
		}
		s.mu.Unlock()
	}
}

type ServerOption func(*Server)

func WithPingInterval(d time.Duration) ServerOption {
	return func(s *Server) {
		s.pingInterval = d
	}
}

func WithPingTimeout(d time.Duration) ServerOption {
	return func(s *Server) {
		s.pingTimeout = d
	}
}

func WithMaxConcurrency(maxConcurrent int) ServerOption {
	return func(s *Server) {
		s.maxConcurrency = maxConcurrent
		s.concurrencySemaphore = make(chan struct{}, maxConcurrent)
	}
}

func WithCompression(enabled bool) ServerOption {
	return func(s *Server) {
		s.compressionEnabled = enabled
	}
}

func WithBufferSize(size int) ServerOption {
	return func(s *Server) {
		s.bufferSize = size
	}
}

func (s *Server) HandleHTTP(w http.ResponseWriter, r *http.Request) {

	if websocket := s.tryWebSocketUpgrade(w, r); websocket != nil {
		s.handleSocket(websocket)
		return
	}

	s.handleLongPolling(w, r)
}

func (s *Server) tryWebSocketUpgrade(w http.ResponseWriter, r *http.Request) Socket {
	if r.Header.Get("Upgrade") != "websocket" {
		return nil
	}

	if s.concurrencySemaphore != nil {
		select {
		case s.concurrencySemaphore <- struct{}{}:

			defer func() {
				<-s.concurrencySemaphore
			}()
		default:

			http.Error(w, "Too many connections", http.StatusServiceUnavailable)
			return nil
		}
	}

	upgraderConfig := transport.Upgrader
	if s.compressionEnabled {
		upgraderConfig.EnableCompression = true
	}
	upgraderConfig.ReadBufferSize = s.bufferSize
	upgraderConfig.WriteBufferSize = s.bufferSize

	conn, err := upgraderConfig.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return nil
	}

	id := generateID()

	wsConfig := transport.DefaultWebSocketServerConfig()
	wsConfig.BufferSize = s.bufferSize

	wsTransport := transport.NewWebSocketServerTransport(
		id,
		conn,
		wsConfig,
	)

	socket := newSocketFromServerTransport(id, wsTransport)

	return socket
}

func (s *Server) handleLongPolling(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	sessionID := r.URL.Query().Get("sessionId")

	switch {
	case path == "/connect" || path == "/socket/connect":
		s.handleLongPollingConnect(w, r)
	case path == "/poll" || path == "/socket/poll":
		s.handleLongPollingPoll(w, r, sessionID)
	case path == "/send" || path == "/socket/send":
		s.handleLongPollingSend(w, r, sessionID)
	case path == "/disconnect" || path == "/socket/disconnect":
		s.handleLongPollingDisconnect(w, r, sessionID)
	default:
		http.Error(w, "Unknown endpoint", http.StatusNotFound)
	}
}

func (s *Server) handleLongPollingConnect(w http.ResponseWriter, _ *http.Request) {
	sessionID := generateID()

	config := transport.DefaultLongPollingServerConfig()
	lpTransport := transport.NewLongPollingServerTransport(sessionID, config)

	socket := newSocketFromServerTransport(sessionID, lpTransport)

	session := &LongPollingSession{
		ID:         sessionID,
		Transport:  lpTransport,
		SocketImpl: socket.(*socketImpl),
	}

	s.mu.Lock()
	s.sessions[sessionID] = session
	s.mu.Unlock()

	go s.registerSocketAndWaitForDisconnect(socket)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"sessionId":"` + sessionID + `"}`))
}

func (s *Server) handleLongPollingPoll(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.mu.RLock()
	session, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	session.Transport.HandlePoll(w, r)
}

func (s *Server) handleLongPollingSend(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.mu.RLock()
	session, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	session.Transport.HandleSend(w, r)
}

func (s *Server) handleLongPollingDisconnect(w http.ResponseWriter, _ *http.Request, sessionID string) {
	s.mu.Lock()
	session, exists := s.sessions[sessionID]
	if exists {
		delete(s.sessions, sessionID)
		delete(s.sockets, session.SocketImpl.id)
	}
	s.mu.Unlock()

	if exists {
		session.SocketImpl.Close()
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) registerSocketAndWaitForDisconnect(socket Socket) {

	s.handleSocket(socket)

	disconnectChan := make(chan struct{})

	socket.On(EventDisconnect, func(data interface{}) {
		close(disconnectChan)
	})

	<-disconnectChan

	log.Printf("Socket %s disconnected, cleaning up", socket.ID())
}

func (s *Server) HandleFunc(event Event, handler func(s Socket, data interface{})) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Printf("Server: Registering handler for event: %s", event)
	s.handlers[event] = append(s.handlers[event], handler)
}

func (s *Server) handleSocket(socket Socket) {

	s.mu.Lock()
	s.sockets[socket.ID()] = socket
	s.mu.Unlock()

	log.Printf("Server: Adding socket %s", socket.ID())

	s.mu.RLock()
	events := make([]Event, 0, len(s.handlers))
	for event := range s.handlers {
		if event != EventConnect && event != EventDisconnect {
			events = append(events, event)
		}
	}
	s.mu.RUnlock()

	for _, event := range events {

		currentEvent := event

		log.Printf("Server: Registering handler on socket %s for event %s", socket.ID(), currentEvent)

		socket.On(currentEvent, func(data interface{}) {
			log.Printf("Server: Socket %s received event %s", socket.ID(), currentEvent)

			s.mu.RLock()
			handlers := s.handlers[currentEvent]
			s.mu.RUnlock()

			for _, handler := range handlers {
				go handler(socket, data)
			}
		})
	}

	socket.On(EventDisconnect, func(data interface{}) {
		log.Printf("Server: Socket %s disconnected", socket.ID())

		s.mu.Lock()
		delete(s.sockets, socket.ID())
		s.mu.Unlock()

		s.LeaveAll(socket.ID())

		s.triggerEvent(socket, EventDisconnect, data)
	})

	s.triggerEvent(socket, EventConnect, nil)
}

func (s *Server) triggerEvent(socket Socket, event Event, data interface{}) {
	s.mu.RLock()
	handlers := s.handlers[event]
	s.mu.RUnlock()

	log.Printf("Server: Triggering event %s for socket %s with %d handlers",
		event, socket.ID(), len(handlers))

	for _, handler := range handlers {
		go handler(socket, data)
	}
}

func (s *Server) Broadcast(event Event, data interface{}) {
	s.mu.RLock()
	socketsCopy := make([]Socket, 0, len(s.sockets))
	for _, socket := range s.sockets {
		socketsCopy = append(socketsCopy, socket)
	}
	s.mu.RUnlock()

	socketCount := len(socketsCopy)
	log.Printf("Server: Broadcasting event %s to %d clients", event, socketCount)

	if socketCount == 0 {
		log.Printf("Server: No sockets connected, broadcast has no effect")
		return
	}

	for _, socket := range socketsCopy {

		go func(s Socket) {
			err := s.Send(event, data)
			if err != nil {
				log.Printf("Server: Error broadcasting to client %s: %v", s.ID(), err)
			}
		}(socket)
	}
}

func (s *Server) BroadcastParallel(event Event, data interface{}) {
	s.mu.RLock()
	socketsCopy := make([]Socket, 0, len(s.sockets))
	for _, socket := range s.sockets {
		socketsCopy = append(socketsCopy, socket)
	}
	s.mu.RUnlock()

	socketCount := len(socketsCopy)
	log.Printf("Server: Broadcasting event %s to %d clients in parallel", event, socketCount)

	if socketCount == 0 {
		log.Printf("Server: No sockets connected, broadcast has no effect")
		return
	}

	var wg sync.WaitGroup
	workerCount := min(socketCount, 20)
	jobs := make(chan Socket, socketCount)

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for socket := range jobs {
				err := socket.Send(event, data)
				if err != nil {
					log.Printf("Server: Error broadcasting to client %s: %v", socket.ID(), err)
				}
			}
		}()
	}

	for _, socket := range socketsCopy {
		jobs <- socket
	}
	close(jobs)

	wg.Wait()
}

func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

func (s *Server) BroadcastToRooms(rooms []string, event Event, data interface{}) {
	for _, room := range rooms {
		s.roomManager.BroadcastToRoom(room, event, data)
	}
}

func (s *Server) BroadcastToRoom(room string, event Event, data interface{}) {
	s.roomManager.BroadcastToRoom(room, event, data)
}

func (s *Server) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.sockets)
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	sockets := make([]Socket, 0, len(s.sockets))
	for _, socket := range s.sockets {
		sockets = append(sockets, socket)
	}
	s.mu.Unlock()

	for _, socket := range sockets {
		if err := socket.Close(); err != nil {
			log.Printf("Error closing socket %s: %v", socket.ID(), err)
		}
	}

	return nil
}

func (s *Server) Join(socketID string, room string) {
	s.mu.RLock()
	socket, exists := s.sockets[socketID]
	s.mu.RUnlock()

	if exists {
		s.roomManager.JoinRoom(room, socket)
	}
}

func (s *Server) Leave(socketID string, room string) {
	s.roomManager.LeaveRoom(room, socketID)
}

func (s *Server) LeaveAll(socketID string) {
	s.roomManager.LeaveAllRooms(socketID)
}

func (s *Server) RoomsOf(socketID string) []string {
	return s.roomManager.GetSocketRooms(socketID)
}

func (s *Server) In(room string) []Socket {
	r := s.roomManager.GetRoom(room)
	return r.GetSockets()
}


func (s *Server) GetSocket(id string) (Socket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	socket, exists := s.sockets[id]
	return socket, exists
}
