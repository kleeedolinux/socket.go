package socket

import (
	"sync"
)

type Room struct {
	name    string
	sockets map[string]Socket
	mu      sync.RWMutex
}

func NewRoom(name string) *Room {
	return &Room{
		name:    name,
		sockets: make(map[string]Socket),
	}
}

func (r *Room) AddSocket(s Socket) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sockets[s.ID()] = s
}

func (r *Room) RemoveSocket(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sockets, id)
}

func (r *Room) HasSocket(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.sockets[id]
	return exists
}

func (r *Room) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sockets)
}

func (r *Room) Broadcast(event Event, data interface{}) {
	r.mu.RLock()
	socketsCopy := make([]Socket, 0, len(r.sockets))
	for _, socket := range r.sockets {
		socketsCopy = append(socketsCopy, socket)
	}
	r.mu.RUnlock()

	for _, socket := range socketsCopy {
		go socket.Send(event, data)
	}
}

func (r *Room) BroadcastParallel(event Event, data interface{}, workerLimit int) {
	r.mu.RLock()
	socketsCopy := make([]Socket, 0, len(r.sockets))
	for _, socket := range r.sockets {
		socketsCopy = append(socketsCopy, socket)
	}
	r.mu.RUnlock()

	socketCount := len(socketsCopy)
	if socketCount == 0 {
		return
	}

	var wg sync.WaitGroup
	workerCount := min(socketCount, workerLimit)
	jobs := make(chan Socket, socketCount)

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for socket := range jobs {
				socket.Send(event, data)
			}
		}()
	}

	for _, socket := range socketsCopy {
		jobs <- socket
	}
	close(jobs)

	wg.Wait()
}

func (r *Room) GetSockets() []Socket {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sockets := make([]Socket, 0, len(r.sockets))
	for _, socket := range r.sockets {
		sockets = append(sockets, socket)
	}

	return sockets
}

func (r *Room) Name() string {
	return r.name
}

type RoomManager struct {
	rooms map[string]*Room
	mu    sync.RWMutex
}

func NewRoomManager() *RoomManager {
	return &RoomManager{
		rooms: make(map[string]*Room),
	}
}

func (rm *RoomManager) GetRoom(name string) *Room {
	rm.mu.RLock()
	room, exists := rm.rooms[name]
	rm.mu.RUnlock()

	if !exists {
		rm.mu.Lock()

		if room, exists = rm.rooms[name]; !exists {
			room = NewRoom(name)
			rm.rooms[name] = room
		}
		rm.mu.Unlock()
	}

	return room
}

func (rm *RoomManager) HasRoom(name string) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	_, exists := rm.rooms[name]
	return exists
}

func (rm *RoomManager) RemoveRoom(name string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	delete(rm.rooms, name)
}

func (rm *RoomManager) GetRooms() []string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	rooms := make([]string, 0, len(rm.rooms))
	for name := range rm.rooms {
		rooms = append(rooms, name)
	}

	return rooms
}

func (rm *RoomManager) JoinRoom(roomName string, socket Socket) {
	room := rm.GetRoom(roomName)
	room.AddSocket(socket)
}

func (rm *RoomManager) LeaveRoom(roomName string, socketID string) {
	rm.mu.RLock()
	room, exists := rm.rooms[roomName]
	rm.mu.RUnlock()

	if exists {
		room.RemoveSocket(socketID)

		if room.Count() == 0 {
			rm.RemoveRoom(roomName)
		}
	}
}

func (rm *RoomManager) LeaveAllRooms(socketID string) {
	rm.mu.RLock()
	roomsCopy := make([]*Room, 0, len(rm.rooms))
	for _, room := range rm.rooms {
		roomsCopy = append(roomsCopy, room)
	}
	rm.mu.RUnlock()

	for _, room := range roomsCopy {
		if room.HasSocket(socketID) {
			room.RemoveSocket(socketID)

			if room.Count() == 0 {
				rm.RemoveRoom(room.Name())
			}
		}
	}
}

func (rm *RoomManager) GetSocketRooms(socketID string) []string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var socketRooms []string
	for name, room := range rm.rooms {
		if room.HasSocket(socketID) {
			socketRooms = append(socketRooms, name)
		}
	}

	return socketRooms
}

func (rm *RoomManager) BroadcastToRoom(roomName string, event Event, data interface{}) {
	rm.mu.RLock()
	room, exists := rm.rooms[roomName]
	rm.mu.RUnlock()

	if exists {
		room.BroadcastParallel(event, data, 10)
	}
}
