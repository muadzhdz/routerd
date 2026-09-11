package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/muadzhdz/routerd/pkg/dns"
	"github.com/muadzhdz/routerd/pkg/telemetry"
)

// CommandHandler represents a callback for client-issued control commands.
type CommandHandler func(args []string) (string, error)

type clientConn struct {
	id         uint64
	conn       net.Conn
	sendChan   chan []byte
	subscribed bool
	closeOnce  sync.Once
	done       chan struct{}
}

func (c *clientConn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// Server manages the Unix Domain Socket listener and telemetry multiplexing.
type Server struct {
	socketPath string
	listener   net.Listener
	mu         sync.RWMutex
	clients    map[uint64]*clientConn
	nextID     uint64
	handlers   map[string]CommandHandler
	closeOnce  sync.Once
	stopChan   chan struct{}
	wg         sync.WaitGroup
}

// NewServer creates a new IPC Server for the given Unix domain socket path.
func NewServer(socketPath string) *Server {
	if socketPath == "" {
		socketPath = GetDefaultSocketPath()
	}
	return &Server{
		socketPath: socketPath,
		clients:    make(map[uint64]*clientConn),
		handlers:   make(map[string]CommandHandler),
		stopChan:   make(chan struct{}),
	}
}

// SocketPath returns the active filesystem path of the socket.
func (s *Server) SocketPath() string {
	return s.socketPath
}

// RegisterCommandHandler registers a callback for a specific command name.
func (s *Server) RegisterCommandHandler(cmd string, handler CommandHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[cmd] = handler
}

// Start binds the Unix socket and begins serving incoming client connections.
func (s *Server) Start() error {
	// Check if another active daemon is already listening on this socket
	if conn, err := net.Dial("unix", s.socketPath); err == nil {
		_ = conn.Close()
		return fmt.Errorf("another routerd daemon is already active on %s", s.socketPath)
	}

	// Remove stale socket file left behind by abnormal termination
	_ = os.Remove(s.socketPath)

	if err := EnsureSocketDirectory(s.socketPath); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on unix socket %s: %w", s.socketPath, err)
	}
	s.listener = l

	// Set permissions: readable and writable by owner and group/others
	_ = os.Chmod(s.socketPath, 0666)

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopChan:
				return
			default:
				continue
			}
		}

		s.mu.Lock()
		s.nextID++
		client := &clientConn{
			id:        s.nextID,
			conn:      conn,
			sendChan:  make(chan []byte, 128),
			done:      make(chan struct{}),
		}
		s.clients[client.id] = client
		s.mu.Unlock()

		s.wg.Add(2)
		go s.clientWriter(client)
		go s.clientReader(client)
	}
}

func (s *Server) clientWriter(c *clientConn) {
	defer s.wg.Done()
	defer c.close()

	for {
		select {
		case <-s.stopChan:
			return
		case <-c.done:
			return
		case data, ok := <-c.sendChan:
			if !ok {
				return
			}
			if _, err := c.conn.Write(data); err != nil {
				return
			}
		}
	}
}

func (s *Server) clientReader(c *clientConn) {
	defer s.wg.Done()
	defer func() {
		c.close()
		s.removeClient(c.id)
	}()

	scanner := bufio.NewScanner(c.conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case TypeSubscribe:
			s.mu.Lock()
			c.subscribed = true
			s.mu.Unlock()
			resp, _ := EncodeMessage(TypeResponse, ResponsePayload{Success: true, Message: "subscribed"})
			s.sendToClient(c, resp)

		case TypePing:
			resp, _ := EncodeMessage(TypePong, "pong")
			s.sendToClient(c, resp)

		case TypeCommand:
			var cmdPayload CommandPayload
			if err := json.Unmarshal(msg.Payload, &cmdPayload); err == nil {
				s.handleCommand(c, cmdPayload)
			}
		}
	}
}

func (s *Server) handleCommand(c *clientConn, cmd CommandPayload) {
	s.mu.RLock()
	handler, found := s.handlers[cmd.Command]
	s.mu.RUnlock()

	var resp ResponsePayload
	if !found {
		resp = ResponsePayload{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", cmd.Command),
		}
	} else {
		out, err := handler(cmd.Args)
		if err != nil {
			resp = ResponsePayload{
				Success: false,
				Message: err.Error(),
			}
		} else {
			resp = ResponsePayload{
				Success: true,
				Message: out,
			}
		}
	}

	encoded, err := EncodeMessage(TypeResponse, resp)
	if err == nil {
		s.sendToClient(c, encoded)
	}
}

func (s *Server) sendToClient(c *clientConn, data []byte) {
	select {
	case c.sendChan <- data:
	default:
		// Drop data if client's buffer is saturated
	}
}

func (s *Server) removeClient(id uint64) {
	s.mu.Lock()
	delete(s.clients, id)
	s.mu.Unlock()
}

// BroadcastSnapshot sends an atomic telemetry snapshot to all subscribed clients without blocking.
func (s *Server) BroadcastSnapshot(snap telemetry.Snapshot) {
	encoded, err := EncodeMessage(TypeSnapshot, snap)
	if err != nil {
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, client := range s.clients {
		if client.subscribed {
			s.sendToClient(client, encoded)
		}
	}
}

// BroadcastDNSEvent sends a real-time DNS resolution event to all subscribed clients without blocking.
func (s *Server) BroadcastDNSEvent(ev dns.DNSEvent) {
	encoded, err := EncodeMessage(TypeDNSEvent, ev)
	if err != nil {
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, client := range s.clients {
		if client.subscribed {
			s.sendToClient(client, encoded)
		}
	}
}

// ConnectedClientsCount returns the current number of attached IPC clients.
func (s *Server) ConnectedClientsCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// Close gracefully closes the socket listener and disconnects all connected clients.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.stopChan)

		if s.listener != nil {
			err = s.listener.Close()
		}

		s.mu.Lock()
		for _, c := range s.clients {
			c.close()
		}
		s.clients = make(map[uint64]*clientConn)
		s.mu.Unlock()

		s.wg.Wait()
		_ = os.Remove(s.socketPath)
	})
	return err
}
