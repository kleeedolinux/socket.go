package distributed

import (
	"context"
	"errors"
	"fmt"
	"math/rand"

	"sync"
	"sync/atomic"
	"time"

	"github.com/solviumdream/socket.go/debug"

	"github.com/solviumdream/socket.go/socket"
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

type ClusterNode struct {
	ID           string
	Address      string
	LastSeen     time.Time
	Load         float64
	Capabilities map[string]int
	mu           sync.RWMutex
}

type Cluster struct {
	LocalNode   *Node
	RemoteNodes map[string]*ClusterNode
	config      ClusterConfig
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
}

type ClusterConfig struct {
	DiscoveryAddresses []string
	HeartbeatInterval  time.Duration
	GossipInterval     time.Duration
	NodeCapabilities   map[string]int
	MaxLoad            float64
}

type ProcessPriority int

const (
	LowPriority ProcessPriority = iota
	NormalPriority
	HighPriority
	CriticalPriority
)

type ProcessOptions struct {
	Priority     ProcessPriority
	MailboxSize  int
	Capabilities map[string]int
}

func DefaultProcessOptions() ProcessOptions {
	return ProcessOptions{
		Priority:     NormalPriority,
		MailboxSize:  100,
		Capabilities: make(map[string]int),
	}
}

func NewCluster(localNode *Node, config ClusterConfig) *Cluster {
	ctx, cancel := context.WithCancel(localNode.ctx)

	cluster := &Cluster{
		LocalNode:   localNode,
		RemoteNodes: make(map[string]*ClusterNode),
		config:      config,
		ctx:         ctx,
		cancel:      cancel,
	}

	go cluster.startDiscovery()
	go cluster.startHeartbeat()
	go cluster.startGossip()
	go cluster.monitorLoad()

	return cluster
}

func (c *Cluster) startDiscovery() {
	for _, addr := range c.config.DiscoveryAddresses {
		c.discoverNode(addr)
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			for _, addr := range c.config.DiscoveryAddresses {
				c.discoverNode(addr)
			}
		}
	}
}

func (c *Cluster) discoverNode(address string) {

	debug.Printf("Discovering node at %s", address)
}

func (c *Cluster) startHeartbeat() {
	ticker := time.NewTicker(c.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.sendHeartbeats()
			c.checkNodeLiveness()
		}
	}
}

func (c *Cluster) sendHeartbeats() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, node := range c.RemoteNodes {
		go func(n *ClusterNode) {

			debug.Printf("Sending heartbeat to node %s", n.ID)
		}(node)
	}
}

func (c *Cluster) checkNodeLiveness() {
	threshold := time.Now().Add(-3 * c.config.HeartbeatInterval)

	c.mu.Lock()
	defer c.mu.Unlock()

	for id, node := range c.RemoteNodes {
		node.mu.RLock()
		lastSeen := node.LastSeen
		node.mu.RUnlock()

		if lastSeen.Before(threshold) {
			debug.Printf("Node %s appears to be down, removing from cluster", id)
			delete(c.RemoteNodes, id)
		}
	}
}

func (c *Cluster) startGossip() {
	ticker := time.NewTicker(c.config.GossipInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.gossipClusterState()
		}
	}
}

func (c *Cluster) gossipClusterState() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	debug.Printf("Gossiping cluster state to peers")
}

func (c *Cluster) monitorLoad() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:

			c.updateLocalLoad()
		}
	}
}

func (c *Cluster) updateLocalLoad() {

	debug.Printf("Updating local node load")
}

func (n *Node) SpawnProcessWithOptions(function ProcessFunc, opts ProcessOptions) (ProcessID, error) {
	return n.SpawnProcessWithIDAndOptions(n.generateProcessID(), function, opts)
}

func (n *Node) SpawnProcessWithIDAndOptions(id ProcessID, function ProcessFunc, opts ProcessOptions) (ProcessID, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.ctx.Err() != nil {
		return "", ErrNodeStopped
	}

	ctx, cancel := context.WithCancel(n.ctx)

	mailboxSize := 100
	if opts.MailboxSize > 0 {
		mailboxSize = opts.MailboxSize
	}

	process := &Process{
		ID:       id,
		Function: function,
		mailbox:  make(chan interface{}, mailboxSize),
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

func (c *Cluster) RemoteCall(nodeID string, to ProcessID, message interface{}) error {
	c.mu.RLock()
	_, exists := c.RemoteNodes[nodeID]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("node %s not found in cluster", nodeID)
	}

	debug.Printf("Remote call to %s on node %s", to, nodeID)
	return nil
}

func (c *Cluster) BroadcastMessage(to ProcessID, message interface{}) map[string]error {
	c.mu.RLock()
	nodes := make(map[string]*ClusterNode, len(c.RemoteNodes))
	for id, node := range c.RemoteNodes {
		nodes[id] = node
	}
	c.mu.RUnlock()

	results := make(map[string]error)
	var wg sync.WaitGroup

	for id := range nodes {
		wg.Add(1)
		go func(nodeID string) {
			defer wg.Done()
			err := c.RemoteCall(nodeID, to, message)
			if err != nil {
				results[nodeID] = err
			}
		}(id)
	}

	wg.Wait()
	return results
}

type DistributedSupervisorSpec struct {
	ID              ProcessID
	Strategy        SupervisorStrategy
	MaxRestarts     int
	RestartPeriod   time.Duration
	LocalProcesses  []ProcessSpec
	RemoteProcesses map[string][]ProcessSpec
}

func NewDistributedSupervisor(spec DistributedSupervisorSpec, cluster *Cluster) error {

	localSpec := SupervisorSpec{
		ID:            spec.ID,
		Strategy:      spec.Strategy,
		MaxRestarts:   spec.MaxRestarts,
		RestartPeriod: spec.RestartPeriod,
		Processes:     spec.LocalProcesses,
	}

	_, err := NewSupervisor(localSpec, cluster.LocalNode)
	if err != nil {
		return err
	}

	for nodeID, processes := range spec.RemoteProcesses {
		cluster.mu.RLock()
		_, exists := cluster.RemoteNodes[nodeID]
		cluster.mu.RUnlock()

		if !exists {
			return fmt.Errorf("node %s not found in cluster", nodeID)
		}

		debug.Printf("Creating remote supervisor on node %s with %d processes", nodeID, len(processes))
	}

	return nil
}

type LoadBalancedProcessGroup struct {
	ID          string
	cluster     *Cluster
	processList []struct {
		nodeID    string
		processID ProcessID
		weight    int
	}
	mu        sync.RWMutex
	strategy  LoadBalancingStrategy
	nextIndex int
}

type LoadBalancingStrategy int

const (
	RoundRobin LoadBalancingStrategy = iota
	WeightedRoundRobin
	LeastConnections
)

func (c *Cluster) NewLoadBalancedGroup(id string, strategy LoadBalancingStrategy) *LoadBalancedProcessGroup {
	return &LoadBalancedProcessGroup{
		ID:      id,
		cluster: c,
		processList: make([]struct {
			nodeID    string
			processID ProcessID
			weight    int
		}, 0),
		strategy:  strategy,
		nextIndex: 0,
	}
}

func (g *LoadBalancedProcessGroup) AddProcess(nodeID string, processID ProcessID, weight int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.processList = append(g.processList, struct {
		nodeID    string
		processID ProcessID
		weight    int
	}{
		nodeID:    nodeID,
		processID: processID,
		weight:    weight,
	})
}

func (g *LoadBalancedProcessGroup) RemoveProcess(nodeID string, processID ProcessID) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for i, proc := range g.processList {
		if proc.nodeID == nodeID && proc.processID == processID {
			g.processList = append(g.processList[:i], g.processList[i+1:]...)
			break
		}
	}
}

func (g *LoadBalancedProcessGroup) Send(message interface{}) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.processList) == 0 {
		return errors.New("no processes available in load balanced group")
	}

	var target struct {
		nodeID    string
		processID ProcessID
		weight    int
	}

	switch g.strategy {
	case RoundRobin:
		target = g.processList[g.nextIndex]
		g.nextIndex = (g.nextIndex + 1) % len(g.processList)

	case WeightedRoundRobin:

		totalWeight := 0
		for _, proc := range g.processList {
			totalWeight += proc.weight
		}

		if totalWeight <= 0 {
			target = g.processList[g.nextIndex]
			g.nextIndex = (g.nextIndex + 1) % len(g.processList)
		} else {

			selection := rand.Intn(totalWeight)
			runningTotal := 0

			for _, proc := range g.processList {
				runningTotal += proc.weight
				if selection < runningTotal {
					target = proc
					break
				}
			}
		}

	case LeastConnections:

		target = g.processList[0]
	}

	if target.nodeID == g.cluster.LocalNode.name {
		return g.cluster.LocalNode.Send(target.processID, message)
	} else {
		return g.cluster.RemoteCall(target.nodeID, target.processID, message)
	}
}

type ProcessStats struct {
	ProcessID       ProcessID
	MessagesTotal   int64
	MessagesPerSec  float64
	AvgProcessingMs float64
	ErrorCount      int64
	LastActive      time.Time
}

func (n *Node) GetProcessStats(id ProcessID) (ProcessStats, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	_, exists := n.processes[id]
	if !exists {
		return ProcessStats{}, ErrProcessNotFound
	}

	return ProcessStats{
		ProcessID:       id,
		MessagesTotal:   0,
		MessagesPerSec:  0,
		AvgProcessingMs: 0,
		ErrorCount:      0,
		LastActive:      time.Now(),
	}, nil
}

type SystemEvent struct {
	Type      SystemEventType
	Timestamp time.Time
	ProcessID ProcessID
	NodeID    string
	Error     error
}

type SystemEventType int

const (
	ProcessStarted SystemEventType = iota
	ProcessTerminated
	NodeJoined
	NodeLeft
	SupervisorRestarting
)

type SystemEventHandler func(event SystemEvent)

func (n *Node) Monitor(handler SystemEventHandler) {

	debug.Printf("Registered system event handler")
}
