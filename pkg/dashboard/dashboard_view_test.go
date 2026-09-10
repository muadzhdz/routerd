package dashboard

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muadzhdz/routerd/pkg/hotspot"
)

func TestDashboardNewFeatures(t *testing.T) {
	cfg := Config{
		WANIface:      "wlp2s0",
		WANIP:         "10.100.2.185",
		HotspotActive: true,
		SSID:          "routerd",
	}
	m := NewModel(cfg)
	m.width = 140
	m.height = 36

	m.clients = []hotspot.ConnectedClient{
		{
			MAC:       "de:47:28:4a:77:58",
			IP:        "10.42.0.38",
			Hostname:  "OPPO-A18",
			Signal:    "-48 dBm",
			TxBitrate: "58.5 MBit/s",
		},
	}
	for i := 1; i <= 30; i++ {
		m.logs = append(m.logs, strings.Repeat("log entry ", 2)+string(rune('A'+(i%26))))
	}
	m.clampHistory[45] = 20
	m.dnsHistory[48] = 15
	m.rxHistory[42] = 50
	m.txHistory[45] = 30
	m.peakClampRate = 20
	m.peakDNSRate = 15
	m.rxPeak = 50 * 1024
	m.txPeak = 30 * 1024

	// 1. Check initial view contains Braille characters
	v1 := m.View()
	if !strings.Contains(v1, "WAN: 10.100.2.185") {
		t.Errorf("View should initially show WAN mode")
	}

	// 2. Switch network mode to LAN using 'n'
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model2 := m2.(Model)
	if model2.netModeIdx != 1 {
		t.Errorf("expected netModeIdx=1 after 'n', got %d", model2.netModeIdx)
	}
	v2 := model2.View()
	if !strings.Contains(v2, "LAN: 10.42.0.1") {
		t.Errorf("View should switch to LAN: 10.42.0.1")
	}

	// 3. Switch back to WAN using 'b'
	m3, _ := model2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	model3 := m3.(Model)
	if model3.netModeIdx != 0 {
		t.Errorf("expected netModeIdx=0 after 'b', got %d", model3.netModeIdx)
	}

	// 4. Test Tab to switch focus to Live Feed
	m4, _ := model3.Update(tea.KeyMsg{Type: tea.KeyTab})
	model4 := m4.(Model)
	if model4.focusPane != 1 {
		t.Errorf("expected focusPane=1 after tab, got %d", model4.focusPane)
	}
	v4 := model4.View()
	if !strings.Contains(v4, "LIVE FEED [ACTIVE · SELECTOR") {
		t.Errorf("View should indicate Live Feed is active with selector")
	}

	// 5. Test moving selector down in Live Feed
	m5, _ := model4.Update(tea.KeyMsg{Type: tea.KeyDown})
	model5 := m5.(Model)
	if model5.selectedLogIdx != 1 {
		t.Errorf("expected selectedLogIdx=1 after Down, got %d", model5.selectedLogIdx)
	}

	// 6. Test End key to jump to latest log
	m6, _ := model5.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model6 := m6.(Model)
	if model6.selectedLogIdx != 999999 {
		t.Errorf("expected selectedLogIdx=999999 after End, got %d", model6.selectedLogIdx)
	}
	v6 := model6.View()
	if !strings.Contains(v6, "▸ ") {
		t.Errorf("View should contain selector cursor '▸ '")
	}

	// 7. Test Box 2 Dual-Card layout
	if !strings.Contains(v1, "Wi-Fi AP (ap0)") || !strings.Contains(v1, "Stealth NAT Pipeline") {
		t.Errorf("View should contain dual-card layout for Box 2")
	}

	// 7. Test width constraints
	lines := strings.Split(v1, "\n")
	for i, line := range lines {
		w := lipgloss.Width(line)
		if w > m.width {
			t.Errorf("line %d width %d exceeds max %d", i, w, m.width)
		}
	}
}
