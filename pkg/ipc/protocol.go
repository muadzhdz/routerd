package ipc

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/muadzhdz/routerd/pkg/dns"
	"github.com/muadzhdz/routerd/pkg/telemetry"
)

// Default paths for the Unix Domain Socket
const (
	DefaultSystemSocketPath = "/run/routerd/routerd.sock"
	DefaultUserSocketPath   = "/tmp/routerd.sock"
)

// Message types for IPC communication
const (
	TypeSubscribe = "subscribe"
	TypeSnapshot  = "snapshot"
	TypeDNSEvent  = "dns_event"
	TypeCommand   = "command"
	TypeResponse  = "response"
	TypePing      = "ping"
	TypePong      = "pong"
)

// GetDefaultSocketPath returns the preferred Unix socket path based on directory access.
// Prefers /run/routerd/routerd.sock if running as root or directory exists,
// falling back to /tmp/routerd.sock for user/test environments.
func GetDefaultSocketPath() string {
	runDir := "/run/routerd"
	if err := os.MkdirAll(runDir, 0755); err == nil {
		return DefaultSystemSocketPath
	}
	return DefaultUserSocketPath
}

// Message represents the top-level framed JSON message over the Unix socket.
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// CommandPayload carries a control command from client to daemon.
type CommandPayload struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// ResponsePayload carries the server response to a control command.
type ResponsePayload struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// EncodeMessage serializes a message into newline-delimited bytes.
func EncodeMessage(msgType string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	envelope := Message{
		Type:    msgType,
		Payload: raw,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeSnapshot decodes a raw JSON payload into a telemetry.Snapshot.
func DecodeSnapshot(raw json.RawMessage) (telemetry.Snapshot, error) {
	var snap telemetry.Snapshot
	err := json.Unmarshal(raw, &snap)
	return snap, err
}

// DecodeDNSEvent decodes a raw JSON payload into a dns.DNSEvent.
func DecodeDNSEvent(raw json.RawMessage) (dns.DNSEvent, error) {
	var ev dns.DNSEvent
	err := json.Unmarshal(raw, &ev)
	return ev, err
}

// EnsureSocketDirectory ensures the directory containing the socket file exists with secure permissions.
func EnsureSocketDirectory(socketPath string) error {
	dir := filepath.Dir(socketPath)
	return os.MkdirAll(dir, 0755)
}
