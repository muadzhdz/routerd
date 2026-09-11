package telemetry

import (
	"testing"

	"github.com/muadzhdz/routerd/pkg/engine"
	"github.com/muadzhdz/routerd/pkg/hotspot"
)

type mockEngineStats struct {
	snap engine.StatsSnapshot
}

func (m *mockEngineStats) Stats() (engine.StatsSnapshot, error) {
	return m.snap, nil
}

func TestCollectorSamplingAndPeaks(t *testing.T) {
	var currentRx uint64 = 10000
	var currentTx uint64 = 5000

	mockEngine := &mockEngineStats{
		snap: engine.StatsSnapshot{
			ClampedPackets: 100,
			ClientHellos:   50,
		},
	}

	cfg := Config{
		WANIface:      "wlp2s0",
		HotspotActive: false,
		StatsProvider: mockEngine,
		NetDevReader: func(iface string) (rx, tx uint64) {
			return currentRx, currentTx
		},
		ClientFetcher: func() ([]hotspot.ConnectedClient, error) {
			return nil, nil
		},
	}

	col := NewCollector(cfg)

	// Step 1: Simulasi lonjakan trafik
	currentRx = 10000 + 20480 // Delta +20KB
	currentTx = 5000 + 10240  // Delta +10KB
	mockEngine.snap.ClampedPackets = 125 // Delta +25

	snap1 := col.Sample()

	if snap1.WAN.RxRate != 20480 {
		t.Errorf("expected RxRate=20480, got %d", snap1.WAN.RxRate)
	}
	if snap1.WAN.TxRate != 10240 {
		t.Errorf("expected TxRate=10240, got %d", snap1.WAN.TxRate)
	}
	if snap1.WAN.RxPeak != 20480 {
		t.Errorf("expected RxPeak=20480, got %d", snap1.WAN.RxPeak)
	}
	if snap1.ClampRate != 25 {
		t.Errorf("expected ClampRate=25, got %d", snap1.ClampRate)
	}
	if snap1.PeakClampRate != 25 {
		t.Errorf("expected PeakClampRate=25, got %d", snap1.PeakClampRate)
	}

	// Cek ring buffer ujung terakhir (offset 47)
	if snap1.WAN.RxHistory[HistorySize-1] != 20 { // 20480 / 1024 = 20 KB/s
		t.Errorf("expected last RxHistory=20, got %d", snap1.WAN.RxHistory[HistorySize-1])
	}

	// Step 2: Trafik melambat, pastikan Peak tetap dipertahankan
	currentRx += 5120 // Delta +5KB
	currentTx += 2048 // Delta +2KB
	mockEngine.snap.ClampedPackets += 5 // Delta +5

	snap2 := col.Sample()

	if snap2.WAN.RxRate != 5120 {
		t.Errorf("expected RxRate=5120, got %d", snap2.WAN.RxRate)
	}
	if snap2.WAN.RxPeak != 20480 {
		t.Errorf("expected Peak to remain 20480, got %d", snap2.WAN.RxPeak)
	}
	if snap2.ClampRate != 5 {
		t.Errorf("expected ClampRate=5, got %d", snap2.ClampRate)
	}
	if snap2.PeakClampRate != 25 {
		t.Errorf("expected PeakClampRate to remain 25, got %d", snap2.PeakClampRate)
	}
}
