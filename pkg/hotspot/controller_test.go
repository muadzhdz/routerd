package hotspot

import (
	"testing"
)

func TestControllerCloseIdempotent(t *testing.T) {
	ctrl := NewController(Config{
		ParentIface: "lo",
		SSID:        "test-ap",
		Password:    "password123",
	})

	// Close on unstarted controller must be safe (no-op)
	if err := ctrl.Close(); err != nil {
		t.Fatalf("close on unstarted controller failed: %v", err)
	}

	// Simulate registered cleanup
	cleaned := false
	ctrl.addCleanup(func() {
		cleaned = true
	})
	ctrl.running = true

	if err := ctrl.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if !cleaned {
		t.Fatal("expected cleanup function to be executed during Close")
	}

	// Idempotent: Second close must not error
	if err := ctrl.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestControllerLIFORollbackOrder(t *testing.T) {
	ctrl := NewController(Config{})

	var executionOrder []int
	ctrl.addCleanup(func() {
		executionOrder = append(executionOrder, 1)
	})
	ctrl.addCleanup(func() {
		executionOrder = append(executionOrder, 2)
	})
	ctrl.addCleanup(func() {
		executionOrder = append(executionOrder, 3)
	})

	ctrl.rollback()

	// Must be executed in LIFO order: 3, 2, 1
	if len(executionOrder) != 3 {
		t.Fatalf("expected 3 cleanups executed, got %d", len(executionOrder))
	}
	if executionOrder[0] != 3 || executionOrder[1] != 2 || executionOrder[2] != 1 {
		t.Errorf("expected order [3, 2, 1], got %v", executionOrder)
	}
}
