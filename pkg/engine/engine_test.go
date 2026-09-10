package engine

import (
	"testing"
)

// FakeStatsProvider memverifikasi bahwa interface StatsProvider dapat di-mock untuk testing non-root.
type FakeStatsProvider struct {
	snapshot StatsSnapshot
	err      error
}

func (f *FakeStatsProvider) Stats() (StatsSnapshot, error) {
	return f.snapshot, f.err
}

func TestStatsProviderInterface(t *testing.T) {
	var provider StatsProvider = &FakeStatsProvider{
		snapshot: StatsSnapshot{
			ClampedPackets: 42,
			ClientHellos:   1337,
			DroppedPackets: 7,
		},
	}

	snap, err := provider.Stats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if snap.ClampedPackets != 42 {
		t.Errorf("expected ClampedPackets=42, got %d", snap.ClampedPackets)
	}
	if snap.ClientHellos != 1337 {
		t.Errorf("expected ClientHellos=1337, got %d", snap.ClientHellos)
	}
	if snap.DroppedPackets != 7 {
		t.Errorf("expected DroppedPackets=7, got %d", snap.DroppedPackets)
	}
}

func TestEngineInvalidInterface(t *testing.T) {
	cfg := Config{
		InterfaceName: "nonexistent_iface_xyz_999",
		XDPMode:       XDPModeDisabled,
	}

	eng, err := Start(cfg)
	if err == nil {
		_ = eng.Close()
		t.Fatal("expected error for non-existent interface, got nil")
	}
}

func TestEngineCloseIdempotent(t *testing.T) {
	eng := &Engine{
		closed: false,
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	// Idempotent: close kedua kali tidak boleh error atau panic
	if err := eng.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}

	// Stats setelah close harus mengembalikan error
	_, err := eng.Stats()
	if err == nil {
		t.Fatal("expected error when querying stats on closed engine, got nil")
	}
}
