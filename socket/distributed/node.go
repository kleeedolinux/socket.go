package distributed

import (
	"context"
	"errors"
	"fmt"

	"sync"
	"sync/atomic"
	"time"

	"github.com/kleeedolinux/socket.go/debug"

	"github.com/kleeedolinux/socket.go/socket"
)

var (
	ErrProcessNotFound = errors.New("process not found")
	ErrNodeStopped     = errors.New("node stopped")
)

type ProcessID string

type Message struct {
	From    ProcessID
	To      ProcessID
	Payload interface{}
}

type ProcessFunc func(ctx context.Context, self ProcessID, node *Node, msg interface{}) error

type Process struct {
	ID       ProcessID
	Function ProcessFunc
	mailbox  chan interface{}
	ctx      context.Context
	cancel   context.CancelFunc
	node     *Node
	alive    atomic.Bool
}

type SupervisorStrategy int

const (
	OneForOne SupervisorStrategy = iota
	OneForAll
	RestForOne
)

type SupervisorSpec struct {
	ID            ProcessID
	Strategy      SupervisorStrategy
	MaxRestarts   int
	RestartPeriod time.Duration
	Processes     []ProcessSpec
}

type ProcessSpec struct {
	ID       ProcessID
	Function ProcessFunc
}

type Node struct {
	mu                sync.RWMutex
	processes         map[ProcessID]*Process
	supervisors       map[ProcessID]*Supervisor
	nextProcessID     atomic.Uint64
	ctx               context.Context
	cancel            context.CancelFunc
	name              string
	roomSubscriptions map[ProcessID]map[string]bool
	SocketServer      *socket.Server
}

type NodeOption func(*Node)

func WithSocketServer(server *socket.Server) NodeOption {
	return func(n *Node) {
		n.SocketServer = server
	}
}

func NewNode(name string, opts ...NodeOption) *Node {
	ctx, cancel := context.WithCancel(context.Background())

	node := &Node{
		processes:         make(map[ProcessID]*Process),
		supervisors:       make(map[ProcessID]*Supervisor),
		ctx:               ctx,
		cancel:            cancel,
		name:              name,
		roomSubscriptions: make(map[ProcessID]map[string]bool),
	}

	for _, opt := range opts {
		opt(node)
	}

	if node.SocketServer != nil {

		node.SocketServer.HandleFunc("join", node.handleRoomJoin)
		node.SocketServer.HandleFunc("leave", node.handleRoomLeave)
		node.SocketServer.HandleFunc(socket.EventDisconnect, node.handleSocketDisconnect)
	}

	return node
}

func (n *Node) handleRoomJoin(s socket.Socket, data interface{}) {
	room, ok := data.(string)
	if !ok {
		return
	}

	n.mu.RLock()
	defer n.mu.RUnlock()

	for pid, rooms := range n.roomSubscriptions {
		if rooms[room] {
			process, exists := n.processes[pid]
			if exists && process.alive.Load() {
				process.mailbox <- map[string]interface{}{
					"type":   "room_join",
					"room":   room,
					"socket": s.ID(),
				}
			}
		}
	}
}

func (n *Node) handleRoomLeave(s socket.Socket, data interface{}) {
	room, ok := data.(string)
	if !ok {
		return
	}

	n.mu.RLock()
	defer n.mu.RUnlock()

	for pid, rooms := range n.roomSubscriptions {
		if rooms[room] {
			process, exists := n.processes[pid]
			if exists && process.alive.Load() {
				process.mailbox <- map[string]interface{}{
					"type":   "room_leave",
					"room":   room,
					"socket": s.ID(),
				}
			}
		}
	}
}

func (n *Node) handleSocketDisconnect(s socket.Socket, data interface{}) {

	n.mu.RLock()
	defer n.mu.RUnlock()

	for pid := range n.processes {
		process, exists := n.processes[pid]
		if exists && process.alive.Load() {
			process.mailbox <- map[string]interface{}{
				"type":   "socket_disconnect",
				"socket": s.ID(),
			}
		}
	}
}

func (n *Node) generateProcessID() ProcessID {
	return ProcessID(n.name + "-" + time.Now().Format("20060102-150405") + "-" +
		fmt.Sprint(n.nextProcessID.Add(1)))
}

func (n *Node) SpawnProcess(function ProcessFunc) (ProcessID, error) {
	return n.SpawnProcessWithID(n.generateProcessID(), function)
}

func (n *Node) SpawnProcessWithID(id ProcessID, function ProcessFunc) (ProcessID, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.ctx.Err() != nil {
		return "", ErrNodeStopped
	}

	ctx, cancel := context.WithCancel(n.ctx)

	process := &Process{
		ID:       id,
		Function: function,
		mailbox:  make(chan interface{}, 100),
		ctx:      ctx,
		cancel:   cancel,
		node:     n,
	}
	process.alive.Store(true)

	n.processes[id] = process

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-process.mailbox:
				if err := function(ctx, id, n, msg); err != nil {
					debug.Printf("Process %s error: %v", id, err)
				}
			}
		}
	}()

	return id, nil
}

func (n *Node) Send(to ProcessID, message interface{}) error {
	n.mu.RLock()
	process, exists := n.processes[to]
	n.mu.RUnlock()

	if !exists || !process.alive.Load() {
		return ErrProcessNotFound
	}

	select {
	case process.mailbox <- message:
		return nil
	default:
		return errors.New("mailbox full")
	}
}

func (n *Node) Kill(id ProcessID) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	process, exists := n.processes[id]
	if !exists {
		return ErrProcessNotFound
	}

	process.cancel()
	process.alive.Store(false)
	delete(n.processes, id)

	delete(n.roomSubscriptions, id)

	return nil
}

func (n *Node) Stop() {
	n.cancel()

	n.mu.Lock()
	defer n.mu.Unlock()

	for id, process := range n.processes {
		process.cancel()
		delete(n.processes, id)
	}
}

func (n *Node) Subscribe(id ProcessID, room string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if _, exists := n.roomSubscriptions[id]; !exists {
		n.roomSubscriptions[id] = make(map[string]bool)
	}

	n.roomSubscriptions[id][room] = true
}

func (n *Node) Unsubscribe(id ProcessID, room string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if rooms, exists := n.roomSubscriptions[id]; exists {
		delete(rooms, room)

		if len(rooms) == 0 {
			delete(n.roomSubscriptions, id)
		}
	}
}

type Supervisor struct {
	ID            ProcessID
	Strategy      SupervisorStrategy
	MaxRestarts   int
	RestartPeriod time.Duration
	Processes     []ProcessSpec
	node          *Node
	restarts      map[ProcessID]*restartTracker
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
}

type restartTracker struct {
	count    int
	lastTime time.Time
}

func NewSupervisor(spec SupervisorSpec, node *Node) (*Supervisor, error) {
	ctx, cancel := context.WithCancel(node.ctx)

	supervisor := &Supervisor{
		ID:            spec.ID,
		Strategy:      spec.Strategy,
		MaxRestarts:   spec.MaxRestarts,
		RestartPeriod: spec.RestartPeriod,
		Processes:     spec.Processes,
		node:          node,
		restarts:      make(map[ProcessID]*restartTracker),
		ctx:           ctx,
		cancel:        cancel,
	}

	for _, procSpec := range spec.Processes {
		if _, err := node.SpawnProcessWithID(procSpec.ID, procSpec.Function); err != nil {
			supervisor.cancel()
			return nil, err
		}
	}

	node.mu.Lock()
	node.supervisors[spec.ID] = supervisor
	node.mu.Unlock()

	go supervisor.monitor()

	return supervisor, nil
}

func (s *Supervisor) monitor() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkProcesses()
		}
	}
}

func (s *Supervisor) checkProcesses() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ctx.Err() != nil {
		return
	}

	s.node.mu.RLock()
	deadProcesses := make([]ProcessID, 0)

	for _, procSpec := range s.Processes {
		process, exists := s.node.processes[procSpec.ID]
		if !exists || !process.alive.Load() {
			deadProcesses = append(deadProcesses, procSpec.ID)
		}
	}
	s.node.mu.RUnlock()

	if len(deadProcesses) == 0 {
		return
	}

	switch s.Strategy {
	case OneForOne:
		for _, pid := range deadProcesses {
			s.restartProcess(pid)
		}
	case OneForAll:
		if len(deadProcesses) > 0 {

			for _, proc := range s.Processes {
				s.restartProcess(proc.ID)
			}
		}
	case RestForOne:
		if len(deadProcesses) > 0 {

			index := -1
			for i, proc := range s.Processes {
				if containsProcessID(deadProcesses, proc.ID) {
					index = i
					break
				}
			}

			if index >= 0 {

				for i := index; i < len(s.Processes); i++ {
					s.restartProcess(s.Processes[i].ID)
				}
			}
		}
	}
}

func (s *Supervisor) restartProcess(id ProcessID) {

	now := time.Now()
	tracker, exists := s.restarts[id]

	if !exists {
		tracker = &restartTracker{
			count:    0,
			lastTime: now,
		}
		s.restarts[id] = tracker
	}

	if now.Sub(tracker.lastTime) > s.RestartPeriod {
		tracker.count = 0
		tracker.lastTime = now
	}

	if tracker.count >= s.MaxRestarts && s.MaxRestarts > 0 {
		debug.Printf("Supervisor %s: Max restarts exceeded for process %s", s.ID, id)
		return
	}

	var procSpec *ProcessSpec
	for i := range s.Processes {
		if s.Processes[i].ID == id {
			procSpec = &s.Processes[i]
			break
		}
	}

	if procSpec == nil {
		return
	}

	s.node.mu.Lock()
	if proc, exists := s.node.processes[id]; exists {
		proc.cancel()
		delete(s.node.processes, id)
	}

	ctx, cancel := context.WithCancel(s.ctx)
	process := &Process{
		ID:       id,
		Function: procSpec.Function,
		mailbox:  make(chan interface{}, 100),
		ctx:      ctx,
		cancel:   cancel,
		node:     s.node,
	}
	process.alive.Store(true)

	s.node.processes[id] = process
	s.node.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-process.mailbox:
				if err := procSpec.Function(ctx, id, s.node, msg); err != nil {
					debug.Printf("Process %s error: %v", id, err)
				}
			}
		}
	}()

	tracker.count++
	tracker.lastTime = now

	debug.Printf("Supervisor %s: Restarted process %s (count: %d/%d)",
		s.ID, id, tracker.count, s.MaxRestarts)
}

func containsProcessID(pids []ProcessID, id ProcessID) bool {
	for _, pid := range pids {
		if pid == id {
			return true
		}
	}
	return false
}
