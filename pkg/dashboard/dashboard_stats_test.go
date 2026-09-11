package dashboard

import (
	"testing"
	"time"

	"github.com/muadzhdz/routerd/pkg/engine"
)

type mockStatsProvider struct {
	snap engine.StatsSnapshot
}

func (m *mockStatsProvider) Stats() (engine.StatsSnapshot, error) {
	return m.snap, nil
}

func TestDashboardStatsProviderIntegration(t *testing.T) {
	mock := &mockStatsProvider{
		snap: engine.StatsSnapshot{
			ClampedPackets: 150,
			ClientHellos:   75,
			DroppedPackets: 10,
		},
	}

	cfg := Config{
		WANIface:      "lo",
		WANIP:         "127.0.0.1",
		StatsProvider: mock,
	}

	model := NewModel(cfg)

	// Send tickMsg to trigger stats polling
	newModel, _ := model.Update(tickMsg(time.Now()))
	m := newModel.(Model)

	if m.totalClampCount != 150 {
		t.Errorf("expected totalClampCount=150, got %d", m.totalClampCount)
	}
	if m.totalHelloCount != 75 {
		t.Errorf("expected totalHelloCount=75, got %d", m.totalHelloCount)
	}
}
