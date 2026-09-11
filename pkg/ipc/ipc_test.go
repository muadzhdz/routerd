package ipc

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/muadzhdz/routerd/pkg/dns"
	"github.com/muadzhdz/routerd/pkg/telemetry"
)

func getTempSocketPath(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "routerd-test-*.sock")
	if err != nil {
		t.Fatalf("failed to create temp socket name: %v", err)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path) // Remove so server can bind it
	return path
}

func TestIPCLifecycle(t *testing.T) {
	sockPath := getTempSocketPath(t)
	defer os.Remove(sockPath)

	srv := NewServer(sockPath)
	srv.RegisterCommandHandler("echo", func(args []string) (string, error) {
		if len(args) == 0 {
			return "empty", nil
		}
		return args[0], nil
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	if !IsDaemonRunning(sockPath) {
		t.Fatal("expected IsDaemonRunning to return true")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := NewClient(sockPath)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("client failed to connect: %v", err)
	}
	defer client.Close()

	// 1. Test Ping / Pong
	if err := client.Ping(1 * time.Second); err != nil {
		t.Fatalf("ping failed: %v", err)
	}

	// 2. Test Command execution
	cmdResp, err := client.SendCommand(ctx, "echo", "hello-routerd")
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	if cmdResp != "hello-routerd" {
		t.Errorf("expected 'hello-routerd', got '%s'", cmdResp)
	}

	// 3. Test Subscribe and Telemetry Broadcast
	snapCh, dnsCh, err := client.Subscribe()
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	testSnap := telemetry.Snapshot{
		ClampedPackets: 42,
		ClientHellos:   17,
		ClampRate:      5,
		WAN: telemetry.BandwidthStats{
			RxRate: 10240,
			TxRate: 5120,
		},
	}
	srv.BroadcastSnapshot(testSnap)

	select {
	case received := <-snapCh:
		if received.ClampedPackets != 42 || received.ClientHellos != 17 {
			t.Errorf("received unexpected snapshot data: %+v", received)
		}
		if received.WAN.RxRate != 10240 {
			t.Errorf("expected RxRate=10240, got %d", received.WAN.RxRate)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for telemetry snapshot")
	}

	// 4. Test DNS Event Broadcast
	testDNS := dns.DNSEvent{
		ClientIP: "10.42.0.15",
		Domain:   "example.com",
		Latency:  25 * time.Millisecond,
		Success:  true,
	}
	srv.BroadcastDNSEvent(testDNS)

	select {
	case received := <-dnsCh:
		if received.ClientIP != "10.42.0.15" || received.Domain != "example.com" {
			t.Errorf("received unexpected DNS event: %+v", received)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for DNS event")
	}
}

func TestIPCStaleSocketRemoval(t *testing.T) {
	sockPath := getTempSocketPath(t)
	defer os.Remove(sockPath)

	// Create a dummy file at the socket path
	if err := os.WriteFile(sockPath, []byte("stale"), 0644); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}

	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatalf("server should remove stale socket and start: %v", err)
	}
	defer srv.Close()

	if !IsDaemonRunning(sockPath) {
		t.Fatal("daemon should be running on cleaned socket path")
	}
}

func TestIPCSlowClientNonBlocking(t *testing.T) {
	sockPath := getTempSocketPath(t)
	defer os.Remove(sockPath)

	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := NewClient(sockPath)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("failed to connect client: %v", err)
	}
	defer client.Close()

	// Subscribe but DO NOT read from channels
	_, _, err := client.Subscribe()
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	// Broadcast 300 snapshots rapidly - server must NOT block
	done := make(chan struct{})
	go func() {
		for i := 0; i < 300; i++ {
			srv.BroadcastSnapshot(telemetry.Snapshot{
				ClampedPackets: uint64(i),
			})
		}
		close(done)
	}()

	select {
	case <-done:
		// Broadcast completed without deadlock
	case <-time.After(1 * time.Second):
		t.Fatal("server broadcast blocked due to slow client buffer")
	}
}

func TestIPCDoubleStartConflict(t *testing.T) {
	sockPath := getTempSocketPath(t)
	defer os.Remove(sockPath)

	srv1 := NewServer(sockPath)
	if err := srv1.Start(); err != nil {
		t.Fatalf("failed to start server 1: %v", err)
	}
	defer srv1.Close()

	// Second server starting on same socket must error with active daemon conflict
	srv2 := NewServer(sockPath)
	err := srv2.Start()
	if err == nil {
		_ = srv2.Close()
		t.Fatal("expected conflict error when starting second server on same socket")
	}
	expectedSubstr := fmt.Sprintf("another routerd daemon is already active on %s", sockPath)
	if err.Error() != expectedSubstr {
		t.Errorf("unexpected error message: %v", err)
	}
}
