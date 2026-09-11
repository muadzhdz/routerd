package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/muadzhdz/routerd/pkg/dns"
	"github.com/muadzhdz/routerd/pkg/telemetry"
)

// Client connects to the routerd IPC daemon over a Unix Domain Socket.
type Client struct {
	socketPath string
	conn       net.Conn
	snapshots  chan telemetry.Snapshot
	dnsEvents  chan dns.DNSEvent
	responses  chan ResponsePayload
	pongs      chan struct{}
	closeOnce  sync.Once
	stopChan   chan struct{}
	wg         sync.WaitGroup
}

// NewClient creates a new IPC Client targeting the given Unix domain socket path.
func NewClient(socketPath string) *Client {
	if socketPath == "" {
		socketPath = GetDefaultSocketPath()
	}
	return &Client{
		socketPath: socketPath,
		snapshots:  make(chan telemetry.Snapshot, 64),
		dnsEvents:  make(chan dns.DNSEvent, 128),
		responses:  make(chan ResponsePayload, 16),
		pongs:      make(chan struct{}, 8),
		stopChan:   make(chan struct{}),
	}
}

// IsDaemonRunning checks whether an active routerd daemon is reachable on the socket path.
func IsDaemonRunning(socketPath string) bool {
	if socketPath == "" {
		socketPath = GetDefaultSocketPath()
	}
	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Connect establishes the connection to the daemon's Unix socket.
func (c *Client) Connect(ctx context.Context) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to routerd daemon at %s: %w", c.socketPath, err)
	}
	c.conn = conn

	c.wg.Add(1)
	go c.readLoop()

	return nil
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	defer func() {
		close(c.snapshots)
		close(c.dnsEvents)
		close(c.responses)
		close(c.pongs)
	}()

	scanner := bufio.NewScanner(c.conn)
	for scanner.Scan() {
		select {
		case <-c.stopChan:
			return
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case TypeSnapshot:
			snap, err := DecodeSnapshot(msg.Payload)
			if err == nil {
				select {
				case c.snapshots <- snap:
				default:
					// Drop older snapshot if consumer is delayed
				}
			}

		case TypeDNSEvent:
			ev, err := DecodeDNSEvent(msg.Payload)
			if err == nil {
				select {
				case c.dnsEvents <- ev:
				default:
				}
			}

		case TypeResponse:
			var resp ResponsePayload
			if err := json.Unmarshal(msg.Payload, &resp); err == nil {
				select {
				case c.responses <- resp:
				default:
				}
			}

		case TypePong:
			select {
			case c.pongs <- struct{}{}:
			default:
			}
		}
	}
}

// Subscribe signals the daemon to begin streaming real-time telemetry snapshots and DNS events.
func (c *Client) Subscribe() (<-chan telemetry.Snapshot, <-chan dns.DNSEvent, error) {
	if c.conn == nil {
		return nil, nil, errors.New("client is not connected")
	}

	encoded, err := EncodeMessage(TypeSubscribe, nil)
	if err != nil {
		return nil, nil, err
	}

	if _, err := c.conn.Write(encoded); err != nil {
		return nil, nil, fmt.Errorf("failed to send subscribe request: %w", err)
	}

	select {
	case resp := <-c.responses:
		if !resp.Success {
			return nil, nil, errors.New(resp.Message)
		}
		return c.snapshots, c.dnsEvents, nil
	case <-time.After(2 * time.Second):
		return nil, nil, errors.New("subscribe timed out waiting for server confirmation")
	case <-c.stopChan:
		return nil, nil, errors.New("client closed")
	}
}

// Ping sends a health check probe to the daemon and waits for a pong.
func (c *Client) Ping(timeout time.Duration) error {
	if c.conn == nil {
		return errors.New("client is not connected")
	}

	encoded, err := EncodeMessage(TypePing, nil)
	if err != nil {
		return err
	}

	if _, err := c.conn.Write(encoded); err != nil {
		return err
	}

	select {
	case <-c.pongs:
		return nil
	case <-time.After(timeout):
		return errors.New("ping timed out")
	case <-c.stopChan:
		return errors.New("client closed")
	}
}

// SendCommand issues a control command to the daemon and returns the string response.
func (c *Client) SendCommand(ctx context.Context, cmd string, args ...string) (string, error) {
	if c.conn == nil {
		return "", errors.New("client is not connected")
	}

	payload := CommandPayload{
		Command: cmd,
		Args:    args,
	}

	encoded, err := EncodeMessage(TypeCommand, payload)
	if err != nil {
		return "", err
	}

	if _, err := c.conn.Write(encoded); err != nil {
		return "", fmt.Errorf("failed to send command %s: %w", cmd, err)
	}

	select {
	case resp := <-c.responses:
		if !resp.Success {
			return "", errors.New(resp.Message)
		}
		return resp.Message, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-c.stopChan:
		return "", errors.New("client closed")
	}
}

// Close disconnects the client and stops the background reader.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.stopChan)
		if c.conn != nil {
			err = c.conn.Close()
		}
		c.wg.Wait()
	})
	return err
}
