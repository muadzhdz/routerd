package dns

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestServerLifecycle(t *testing.T) {
	// Bind to ephemeral port on localhost
	srv, err := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		QueryTimeout: 1 * time.Second,
		CacheEntries: 100,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Verify events channel is non-nil
	events := srv.Events()
	if events == nil {
		t.Fatal("expected non-nil events channel")
	}

	// Idempotent clean shutdown
	if err := srv.Close(); err != nil {
		t.Fatalf("failed to close server: %v", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestServerAdBlockSinkhole(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		QueryTimeout: 1 * time.Second,
		CacheEntries: 100,
		BlockAds:     true,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	if srv.Filter() == nil {
		t.Fatal("expected non-nil filter on server")
	}

	srv.Filter().Add("doubleclick.net")
	srv.Filter().Add("analytics.tiktok.com")

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	// Connect client socket to server's ephemeral UDP port
	srvAddr := srv.pc.LocalAddr().String()
	conn, err := net.Dial("udp", srvAddr)
	if err != nil {
		t.Fatalf("failed to dial server: %v", err)
	}
	defer conn.Close()

	// Send query for blocked domain: ad.doubleclick.net
	query := buildMockQuery("ad.doubleclick.net", TypeA, 0x1337)
	if _, err := conn.Write(query); err != nil {
		t.Fatalf("failed to send query: %v", err)
	}

	buf := make([]byte, 512)
	_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	resp := buf[:n]
	tid := binary.BigEndian.Uint16(resp[0:2])
	if tid != 0x1337 {
		t.Errorf("expected TID=0x1337, got 0x%x", tid)
	}

	ancount := binary.BigEndian.Uint16(resp[6:8])
	if ancount != 1 {
		t.Errorf("expected ANCOUNT=1, got %d", ancount)
	}

	// Verify last 4 bytes are 0.0.0.0
	rdata := resp[len(resp)-4:]
	if rdata[0] != 0 || rdata[1] != 0 || rdata[2] != 0 || rdata[3] != 0 {
		t.Errorf("expected 0.0.0.0 sinkhole IP, got %v", rdata)
	}

	// Check event was emitted with (blocked) label
	select {
	case ev := <-srv.Events():
		if ev.Domain != "ad.doubleclick.net (blocked)" {
			t.Errorf("expected event domain 'ad.doubleclick.net (blocked)', got '%s'", ev.Domain)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for sinkhole event")
	}
}
