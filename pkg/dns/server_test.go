package dns

import (
	"testing"
	"time"
)

func TestServerLifecycle(t *testing.T) {
	// Bind ke port ephemeral di localhost
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

	// Pastikan events channel aktif
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
