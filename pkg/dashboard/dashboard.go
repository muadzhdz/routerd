package dashboard

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muadzhdz/routerd/pkg/dns"
	"github.com/muadzhdz/routerd/pkg/engine"
	"github.com/muadzhdz/routerd/pkg/hotspot"
	"github.com/muadzhdz/routerd/pkg/telemetry"
)

var sparkBlocks = []rune{' ', ' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// Config carries initial parameters for dashboard initialization.
type Config struct {
	WANIface       string
	WANIP          string
	HotspotActive  bool
	SSID           string
	VPNActive      bool
	VPNIface       string
	StatsProvider  engine.StatsProvider
	DNSEventChan   <-chan dns.DNSEvent
	SnapshotChan   <-chan telemetry.Snapshot
	IsRemoteClient bool
	ShutdownFunc   func()
}

// Model represents the complete state of the TUI dashboard (The Elm Architecture).
type Model struct {
	cfg       Config
	collector *telemetry.Collector
	width     int
	height    int
	startTime time.Time

	// Counters
	prevClampCount  uint64
	totalClampCount uint64
	clampRate       int
	peakClampRate   int
	clampHistory    []int

	prevHelloCount  uint64
	totalHelloCount uint64

	totalDNSCount uint64
	dnsRate       int
	peakDNSRate   int
	avgDNSLatency time.Duration
	dnsHistory    []int

	// Bandwidth Telemetry (from /proc/net/dev)
	prevRxBytes uint64
	prevTxBytes uint64
	totalRx     uint64
	totalTx     uint64
	rxRate      uint64 // B/s
	txRate      uint64 // B/s
	rxPeak      uint64
	txPeak      uint64
	rxHistory   []int // in KB/s
	txHistory   []int // in KB/s

	// LAN / Hotspot Bandwidth Telemetry (ap0)
	prevApRx    uint64
	prevApTx    uint64
	totalApRx   uint64
	totalApTx   uint64
	apRxRate    uint64
	apTxRate    uint64
	apRxPeak    uint64
	apTxPeak    uint64
	apRxHistory []int // in KB/s
	apTxHistory []int // in KB/s

	// Interactivity & Focus
	focusPane        int // 0 = STATIONS, 1 = LIVE FEED
	selectedIdx      int
	selectedLogIdx   int
	logViewportStart int
	netModeIdx       int // 0 = WAN (wlp2s0), 1 = LAN Hotspot (ap0)

	clients        []hotspot.ConnectedClient
	filterClientIP string
	logs           []string
	quitting       bool
}

type tickMsg time.Time
type dnsMsg dns.DNSEvent
type snapshotMsg telemetry.Snapshot

func tickCmd() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func waitForDNSEvent(ch <-chan dns.DNSEvent) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return dnsMsg(ev)
	}
}

func waitForSnapshot(ch <-chan telemetry.Snapshot) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		snap, ok := <-ch
		if !ok {
			return nil
		}
		return snapshotMsg(snap)
	}
}

// NewModel creates a new instance of the dashboard Model.
func NewModel(cfg Config) Model {
	histLen := 50
	var col *telemetry.Collector
	if cfg.SnapshotChan == nil {
		col = telemetry.NewCollector(telemetry.Config{
			WANIface:      cfg.WANIface,
			HotspotActive: cfg.HotspotActive,
			StatsProvider: cfg.StatsProvider,
		})
	}

	initLog := fmt.Sprintf("[%s] Engine initialized. All eBPF hooks mounted.", time.Now().Format("15:04:05"))
	if cfg.IsRemoteClient {
		initLog = fmt.Sprintf("[%s] Attached to routerd daemon via Unix socket.", time.Now().Format("15:04:05"))
	}

	return Model{
		cfg:          cfg,
		collector:    col,
		startTime:    time.Now(),
		clampHistory: make([]int, histLen),
		dnsHistory:   make([]int, histLen),
		rxHistory:    make([]int, histLen),
		txHistory:    make([]int, histLen),
		apRxHistory:  make([]int, histLen),
		apTxHistory:  make([]int, histLen),
		clients:      []hotspot.ConnectedClient{},
		logs:         []string{initLog},
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		waitForDNSEvent(m.cfg.DNSEventChan),
	}
	if m.cfg.SnapshotChan != nil {
		cmds = append(cmds, waitForSnapshot(m.cfg.SnapshotChan))
	} else {
		cmds = append(cmds, tickCmd())
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			if m.cfg.ShutdownFunc != nil {
				m.cfg.ShutdownFunc()
			}
			return m, tea.Quit
		case "tab":
			m.focusPane = (m.focusPane + 1) % 2
		case "b", "n":
			if m.cfg.HotspotActive {
				m.netModeIdx = (m.netModeIdx + 1) % 2
			}
		case "c":
			m.logs = []string{fmt.Sprintf("[%s] Activity buffer cleared.", time.Now().Format("15:04:05"))}
			m.selectedLogIdx = 0
			m.logViewportStart = 0
		case "r":
			if m.cfg.HotspotActive {
				clients, _ := hotspot.GetConnectedClients()
				m.clients = clients
			}
		case "up", "k":
			if m.focusPane == 0 {
				if m.selectedIdx > 0 {
					m.selectedIdx--
				}
			} else {
				if m.selectedLogIdx > 0 {
					m.selectedLogIdx--
				}
			}
		case "down", "j":
			if m.focusPane == 0 {
				if m.selectedIdx < len(m.clients)-1 {
					m.selectedIdx++
				}
			} else {
				m.selectedLogIdx++
			}
		case "pgup":
			if m.focusPane == 1 {
				m.selectedLogIdx -= 6
				if m.selectedLogIdx < 0 {
					m.selectedLogIdx = 0
				}
			}
		case "pgdown":
			if m.focusPane == 1 {
				m.selectedLogIdx += 6
			}
		case "end", "G":
			if m.focusPane == 1 {
				m.selectedLogIdx = 999999
			}
		case "home", "g":
			if m.focusPane == 1 {
				m.selectedLogIdx = 0
			}
		case "enter":
			if m.focusPane == 0 {
				if len(m.clients) > 0 && m.selectedIdx >= 0 && m.selectedIdx < len(m.clients) {
					targetIP := m.clients[m.selectedIdx].IP
					if m.filterClientIP == targetIP {
						m.filterClientIP = "" // toggle off
					} else {
						m.filterClientIP = targetIP // toggle on
					}
					m.selectedLogIdx = 0
					m.logViewportStart = 0
				}
			}
		case "esc":
			m.filterClientIP = ""
			m.selectedLogIdx = 0
			m.logViewportStart = 0
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case snapshotMsg:
		snap := telemetry.Snapshot(msg)
		m.applySnapshot(snap)
		m.dnsHistory = append(m.dnsHistory[1:], m.dnsRate)
		m.dnsRate = 0
		if m.cfg.SnapshotChan != nil {
			cmds = append(cmds, waitForSnapshot(m.cfg.SnapshotChan))
		}

	case tickMsg:
		if m.collector != nil {
			snap := m.collector.Sample()
			m.applySnapshot(snap)
		}

		m.dnsHistory = append(m.dnsHistory[1:], m.dnsRate)
		m.dnsRate = 0

		cmds = append(cmds, tickCmd())

	case dnsMsg:
		m.totalDNSCount++
		m.dnsRate++
		if m.dnsRate > m.peakDNSRate {
			m.peakDNSRate = m.dnsRate
		}
		if m.avgDNSLatency == 0 {
			m.avgDNSLatency = msg.Latency
		} else {
			m.avgDNSLatency = (m.avgDNSLatency*4 + msg.Latency) / 5
		}

		statusStr := "OK"
		if !msg.Success {
			statusStr = "FAIL"
		}
		logLine := fmt.Sprintf("[%s] DNS %-15s -> %-24s (%v) [%s]",
			time.Now().Format("15:04:05"),
			msg.ClientIP,
			msg.Domain,
			msg.Latency.Round(time.Millisecond),
			statusStr,
		)
		m.addLog(logLine)

		cmds = append(cmds, waitForDNSEvent(m.cfg.DNSEventChan))
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) addLog(entry string) {
	wasAtEnd := (m.selectedLogIdx >= len(m.logs)-1) || (m.selectedLogIdx == 0 && len(m.logs) == 0)
	m.logs = append(m.logs, entry)
	maxLogs := 100
	if len(m.logs) > maxLogs {
		m.logs = m.logs[len(m.logs)-maxLogs:]
		if m.selectedLogIdx > 0 {
			m.selectedLogIdx--
		}
	}
	if wasAtEnd {
		m.selectedLogIdx = len(m.logs) - 1
	}
}

func (m *Model) applySnapshot(snap telemetry.Snapshot) {
	m.totalClampCount = snap.ClampedPackets
	m.clampRate = snap.ClampRate
	m.peakClampRate = snap.PeakClampRate
	if len(snap.ClampHistory) > 0 {
		m.clampHistory = snap.ClampHistory
	}

	m.totalHelloCount = snap.ClientHellos

	m.totalRx = snap.WAN.RxBytes
	m.totalTx = snap.WAN.TxBytes
	m.rxRate = snap.WAN.RxRate
	m.txRate = snap.WAN.TxRate
	m.rxPeak = snap.WAN.RxPeak
	m.txPeak = snap.WAN.TxPeak
	if len(snap.WAN.RxHistory) > 0 {
		m.rxHistory = snap.WAN.RxHistory
	}
	if len(snap.WAN.TxHistory) > 0 {
		m.txHistory = snap.WAN.TxHistory
	}

	if m.cfg.HotspotActive || snap.LAN.RxBytes > 0 || len(snap.Clients) > 0 {
		m.totalApRx = snap.LAN.RxBytes
		m.totalApTx = snap.LAN.TxBytes
		m.apRxRate = snap.LAN.RxRate
		m.apTxRate = snap.LAN.TxRate
		m.apRxPeak = snap.LAN.RxPeak
		m.apTxPeak = snap.LAN.TxPeak
		if len(snap.LAN.RxHistory) > 0 {
			m.apRxHistory = snap.LAN.RxHistory
		}
		if len(snap.LAN.TxHistory) > 0 {
			m.apTxHistory = snap.LAN.TxHistory
		}
		m.clients = snap.Clients
		if m.selectedIdx >= len(m.clients) && len(m.clients) > 0 {
			m.selectedIdx = len(m.clients) - 1
		}
	}
}


func formatBytes(b uint64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatRate(bps uint64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case bps >= gb:
		return fmt.Sprintf("%.2f GiB/s", float64(bps)/float64(gb))
	case bps >= mb:
		val := float64(bps) / float64(mb)
		if val >= 100 {
			return fmt.Sprintf("%.0f MiB/s", val)
		} else if val >= 10 {
			return fmt.Sprintf("%.1f MiB/s", val)
		}
		return fmt.Sprintf("%.2f MiB/s", val)
	case bps >= kb:
		val := float64(bps) / float64(kb)
		if val >= 100 {
			return fmt.Sprintf("%.0f KiB/s", val)
		} else if val >= 10 {
			return fmt.Sprintf("%.1f KiB/s", val)
		}
		return fmt.Sprintf("%.2f KiB/s", val)
	default:
		return fmt.Sprintf("%d Byte/s", bps)
	}
}

func formatBits(bytes uint64) string {
	bits := bytes * 8
	const (
		kb = 1000
		mb = kb * 1000
		gb = mb * 1000
	)
	switch {
	case bits >= gb:
		return fmt.Sprintf("%.2f Gibps", float64(bits)/float64(gb))
	case bits >= mb:
		val := float64(bits) / float64(mb)
		if val >= 100 {
			return fmt.Sprintf("%.0f Mibps", val)
		} else if val >= 10 {
			return fmt.Sprintf("%.1f Mibps", val)
		}
		return fmt.Sprintf("%.2f Mibps", val)
	case bits >= kb:
		val := float64(bits) / float64(kb)
		if val >= 100 {
			return fmt.Sprintf("%.0f Kibps", val)
		} else if val >= 10 {
			return fmt.Sprintf("%.1f Kibps", val)
		}
		return fmt.Sprintf("%.2f Kibps", val)
	default:
		return fmt.Sprintf("%d bps", bits)
	}
}

func formatShortScale(b uint64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	if b <= 10*kb {
		return "10K"
	}
	switch {
	case b >= gb:
		return fmt.Sprintf("%dG", b/gb)
	case b >= mb:
		return fmt.Sprintf("%dM", b/mb)
	case b >= kb:
		return fmt.Sprintf("%dK", b/kb)
	default:
		return "10K"
	}
}

// makeGaugeBar generates a btop progress bar: [██████░░░░░░]
func makeGaugeBar(val, max int, width int) string {
	if width <= 2 {
		return ""
	}
	barLen := width - 2
	if max <= 0 {
		return "[" + strings.Repeat("░", barLen) + "]"
	}
	ratio := float64(val) / float64(max)
	if ratio > 1.0 {
		ratio = 1.0
	}
	if ratio < 0.0 {
		ratio = 0.0
	}
	filled := int(ratio * float64(barLen))
	empty := barLen - filled
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", empty) + "]"
}

// makeSolidMeter generates a compact high-resolution meter bar using ▰ and ▱ characters.
func makeSolidMeter(val, max int, width int) string {
	if width <= 0 {
		return ""
	}
	if max <= 0 {
		return strings.Repeat("▱", width)
	}
	ratio := float64(val) / float64(max)
	if ratio > 1.0 {
		ratio = 1.0
	}
	if ratio < 0.0 {
		ratio = 0.0
	}
	filled := int(ratio * float64(width))
	empty := width - filled
	return strings.Repeat("▰", filled) + strings.Repeat("▱", empty)
}

// renderSparkline transforms a value slice into a Unicode block graph.
func renderSparkline(values []int, width int) string {
	if len(values) == 0 || width <= 0 {
		return ""
	}
	slice := values
	if len(slice) > width {
		slice = slice[len(slice)-width:]
	}

	max := 1
	for _, v := range slice {
		if v > max {
			max = v
		}
	}

	var sb strings.Builder
	for _, v := range slice {
		idx := int((float64(v) / float64(max)) * float64(len(sparkBlocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkBlocks) {
			idx = len(sparkBlocks) - 1
		}
		sb.WriteRune(sparkBlocks[idx])
	}
	if len(slice) < width {
		sb.WriteString(strings.Repeat(" ", width-len(slice)))
	}
	return sb.String()
}

// renderBtopBox renders a box panel in btop style seamlessly integrated into borders.
func renderBtopBox(width, height int, tabs []string, centerTitle string, rightTitle string, contentLines []string) string {
	if width < 12 || height < 3 {
		return ""
	}

	var sb strings.Builder

	// 1. Top border with clean embedded tags (NO downward vertical stems)
	// Format: ┌──[ tab1 ]──[ tab2 ]──────────────── center ──────────────── right ──┐
	var header strings.Builder
	header.WriteString("┌")
	for _, t := range tabs {
		header.WriteString("──[ " + t + " ]")
	}
	if len(tabs) == 0 {
		header.WriteString("──")
	}

	headStr := header.String()
	headW := runewidth.StringWidth(headStr)

	if headW >= width-4 {
		headStr = runewidth.Truncate(headStr, width-4, "")
		sb.WriteString(headStr + "──┐\n")
	} else {
		// Adaptively fit rightTitle based on available width
		candidates := []string{rightTitle}
		if strings.Contains(rightTitle, "sync auto zero ") {
			candidates = append(candidates, strings.Replace(rightTitle, "sync auto zero ", "", 1))
		}

		var rightDec string
		var rightW int
		for _, cand := range candidates {
			if cand != "" {
				rightDec = " " + cand + " ──"
			} else {
				rightDec = "──"
			}
			rightW = lipgloss.Width(rightDec)
			if headW+rightW < width-4 {
				break
			}
		}
		if headW+rightW >= width-4 {
			rightDec = "──"
			rightW = 2
		}

		var centerDec string
		if centerTitle != "" {
			centerDec = " " + centerTitle + " "
		}
		centerW := lipgloss.Width(centerDec)

		rem := width - headW - rightW - centerW - 1
		if rem < 2 {
			centerDec = ""
			rem = width - headW - rightW - 1
			if rem < 0 {
				rem = 0
			}
		}

		leftFill := rem / 2
		rightFill := rem - leftFill
		sb.WriteString(headStr)
		sb.WriteString(strings.Repeat("─", leftFill))
		sb.WriteString(centerDec)
		sb.WriteString(strings.Repeat("─", rightFill))
		sb.WriteString(rightDec)
		sb.WriteString("┐\n")
	}

	// 2. Middle content lines
	innerWidth := width - 4
	innerRows := height - 2
	for r := 0; r < innerRows; r++ {
		line := ""
		if r < len(contentLines) {
			line = contentLines[r]
		}
		lineW := lipgloss.Width(line)
		if lineW > innerWidth {
			line = runewidth.Truncate(line, innerWidth, "…")
			lineW = lipgloss.Width(line)
		}
		pad := innerWidth - lineW
		if pad < 0 {
			pad = 0
		}
		sb.WriteString("│ " + line + strings.Repeat(" ", pad) + " │\n")
	}

	// 3. Bottom border
	botFill := width - 2
	sb.WriteString("└" + strings.Repeat("─", botFill) + "┘")

	return sb.String()
}

func makeLegendRow(innerW int, left, right string) string {
	contentW := lipgloss.Width(left) + lipgloss.Width(right)
	pad := innerW - contentW
	if pad < 1 {
		pad = 1
	}
	return "│ " + left + strings.Repeat(" ", pad) + right + " │"
}

var brailleMap = [4][2]int{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// renderBidirectionalBraille generates high-resolution Braille graph lines (RX/Clamp upwards, TX/DNS downwards).
func renderBidirectionalBraille(width, height int, topData, botData []int, maxTop, maxBot int) []string {
	if maxTop <= 0 {
		maxTop = 10
	}
	if maxBot <= 0 {
		maxBot = 10
	}
	if width <= 0 || height <= 2 {
		return make([]string, height)
	}

	subW := width * 2
	mid := height / 2
	topSubH := mid * 4
	botSubH := (height - 1 - mid) * 4

	topDots := make([]int, subW)
	botDots := make([]int, subW)

	for i := 0; i < subW; i++ {
		dataIdx := len(topData) - subW + i
		valTop := 0
		valBot := 0
		if dataIdx >= 0 && dataIdx < len(topData) {
			valTop = topData[dataIdx]
		}
		if dataIdx >= 0 && dataIdx < len(botData) {
			valBot = botData[dataIdx]
		}
		topDots[i] = int(float64(valTop) / float64(maxTop) * float64(topSubH))
		if topDots[i] > topSubH {
			topDots[i] = topSubH
		}
		botDots[i] = int(float64(valBot) / float64(maxBot) * float64(botSubH))
		if botDots[i] > botSubH {
			botDots[i] = botSubH
		}
	}

	lines := make([]string, height)
	for r := 0; r < height; r++ {
		var sb strings.Builder
		if r < mid {
			for c := 0; c < width; c++ {
				mask := 0
				for dotR := 0; dotR < 4; dotR++ {
					subY := (mid-1-r)*4 + (3 - dotR) + 1
					for dotC := 0; dotC < 2; dotC++ {
						sx := c*2 + dotC
						if sx < subW && topDots[sx] >= subY {
							mask |= brailleMap[dotR][dotC]
						}
					}
				}
				if mask == 0 {
					sb.WriteRune(' ')
				} else {
					sb.WriteRune(rune(0x2800 + mask))
				}
			}
		} else if r == mid {
			for c := 0; c < width; c++ {
				sx0 := c * 2
				sx1 := c*2 + 1
				hasTraffic := (sx0 < subW && (topDots[sx0] > 0 || botDots[sx0] > 0)) ||
					(sx1 < subW && (topDots[sx1] > 0 || botDots[sx1] > 0))
				if hasTraffic {
					sb.WriteString("┼")
				} else {
					sb.WriteString("┄")
				}
			}
		} else {
			for c := 0; c < width; c++ {
				mask := 0
				for dotR := 0; dotR < 4; dotR++ {
					subY := (r-mid-1)*4 + dotR + 1
					for dotC := 0; dotC < 2; dotC++ {
						sx := c*2 + dotC
						if sx < subW && botDots[sx] >= subY {
							mask |= brailleMap[dotR][dotC]
						}
					}
				}
				if mask == 0 {
					sb.WriteRune(' ')
				} else {
					sb.WriteRune(rune(0x2800 + mask))
				}
			}
		}
		lines[r] = sb.String()
	}
	return lines
}

// renderEbpfBox renders the eBPF & DPI Scrambler panel with high-resolution Braille waveforms and kernel telemetry.
func renderEbpfBox(width, height int, currTime string, uptime time.Duration, wanIface string,
	clampRate, peakClampRate int, totalClamp uint64, clampHistory []int,
	dnsRate, peakDNSRate int, totalDNS uint64, avgDNSLatency time.Duration, dnsHistory []int,
	totalHello uint64) string {

	innerW := width - 4
	innerH := height - 2
	if innerW < 40 || innerH < 4 {
		return renderBtopBox(width, height, []string{"¹ebpf-tcx", "dpi-scrambler"}, currTime, "", nil)
	}

	// 1. Horizontal split: 50% for Braille Waveform, 50% for Kernel Telemetry Matrix
	graphSectionW := int(float64(innerW) * 0.50)
	matrixW := innerW - graphSectionW - 3
	if matrixW < 36 {
		matrixW = 36
		graphSectionW = innerW - matrixW - 3
	}

	scaleW := 4
	graphW := graphSectionW - scaleW - 1
	if graphW < 4 {
		graphW = 4
	}

	maxClamp := peakClampRate
	if maxClamp <= 0 {
		maxClamp = 10
	}
	maxDNS := peakDNSRate
	if maxDNS <= 0 {
		maxDNS = 10
	}

	brailleLines := renderBidirectionalBraille(graphW, innerH, clampHistory, dnsHistory, maxClamp, maxDNS)

	// 2. Format Kernel Telemetry Matrix (Right side) - High-tech, Clean, No Ugly Dots
	clampMeter := makeSolidMeter(clampRate, 30, 8)
	dnsMeter := makeSolidMeter(dnsRate, 30, 8)
	helloMeter := makeSolidMeter(int(totalHello%30), 30, 8)

	matrixLines := []string{
		fmt.Sprintf("TCX Ingress   Hook [%s] -> ACTIVE (RFC 1624)", wanIface),
		fmt.Sprintf("TCX Egress    Hook [%s] -> ACTIVE (DPI Guard)", wanIface),
		fmt.Sprintf("SYN Clamping  %s %3d/s · Total: %4d pkts", clampMeter, clampRate, totalClamp),
		fmt.Sprintf("DoH Resolver  %s %3d/s · Total: %4d reqs", dnsMeter, dnsRate, totalDNS),
		fmt.Sprintf("TLS Scramble  %s %3d/s · Total: %4d pkts", helloMeter, 0, totalHello),
		fmt.Sprintf("DoH Latency   %-6v (Cloudflare Wire-Format)", avgDNSLatency.Round(time.Millisecond)),
		"Upstream DNS  1.1.1.1:443 (DoH RFC 8484)",
		"Wi-Fi Shield  XDP Native Protection ENABLED",
	}

	matrixLen := len(matrixLines)
	matrixPadTop := (innerH - matrixLen) / 2
	if matrixPadTop < 0 {
		matrixPadTop = 0
	}

	maxLineW := 0
	for _, l := range matrixLines {
		if w := runewidth.StringWidth(l); w > maxLineW {
			maxLineW = w
		}
	}
	matrixPadLeft := (matrixW - maxLineW) / 2
	if matrixPadLeft < 0 {
		matrixPadLeft = 0
	}

	var contentLines []string
	for r := 0; r < innerH; r++ {
		// Left side: Scale + Braille Waveform
		scaleStr := "    "
		if r == 0 {
			scaleStr = fmt.Sprintf("%-4s", fmt.Sprintf("%dp", maxClamp))
		} else if r == innerH-1 {
			scaleStr = fmt.Sprintf("%-4s", fmt.Sprintf("%dr", maxDNS))
		}
		gStr := brailleLines[r]
		leftSide := fmt.Sprintf("%s %s", scaleStr, gStr)
		leftSide = runewidth.Truncate(leftSide, graphSectionW, "")
		padL := graphSectionW - lipgloss.Width(leftSide)
		if padL > 0 {
			leftSide += strings.Repeat(" ", padL)
		}

		// Right side: Centered Kernel Matrix line
		mStr := ""
		lineIdx := r - matrixPadTop
		if lineIdx >= 0 && lineIdx < matrixLen {
			mStr = strings.Repeat(" ", matrixPadLeft) + matrixLines[lineIdx]
		}
		mStr = runewidth.Truncate(mStr, matrixW, "…")
		padR := matrixW - lipgloss.Width(mStr)
		if padR > 0 {
			mStr += strings.Repeat(" ", padR)
		}

		contentLines = append(contentLines, leftSide+" │ "+mStr)
	}

	topTabs := []string{"¹ebpf-tcx", "dpi-scrambler"}
	topRight := fmt.Sprintf("WAN: %s │ Uptime: %s", wanIface, uptime.Round(time.Second))
	return renderBtopBox(width, height, topTabs, currTime, topRight, contentLines)
}

// renderBtopNetBox renders the network box with dual Braille waveforms and floating legend box matching btop layout.
func renderBtopNetBox(width, height int, modeLabel, ifaceName, ipStr string, rxRate, txRate, rxPeak, txPeak, totalRx, totalTx uint64, rxHist, txHist []int) string {
	innerW := width - 4
	innerH := height - 2
	if innerW < 44 || innerH < 4 {
		tabs := []string{"³net", modeLabel + ": " + ipStr}
		return renderBtopBox(width, height, tabs, "", ifaceName, nil)
	}

	// 1. Floating Legend Box Design (33 characters wide, matching btop layout)
	legendW := 33
	innerLegendW := legendW - 4
	topTitle := "download (" + modeLabel + ")"
	botTitle := "upload (" + modeLabel + ")"
	topFill := legendW - 2 - 1 - len(topTitle)
	if topFill < 1 {
		topFill = 1
	}
	botFill := legendW - 2 - 1 - len(botTitle)
	if botFill < 1 {
		botFill = 1
	}

	topBorder := "┌─" + topTitle + strings.Repeat("─", topFill) + "┐"
	botBorder := "└─" + botTitle + strings.Repeat("─", botFill) + "┘"

	legend := []string{
		topBorder,
		makeLegendRow(innerLegendW, fmt.Sprintf("▼ %s", formatRate(rxRate)), fmt.Sprintf("(%s)", formatBits(rxRate))),
		makeLegendRow(innerLegendW, "▼ Top:", fmt.Sprintf("(%s)", formatBits(rxPeak))),
		makeLegendRow(innerLegendW, "▼ Total:", formatBytes(totalRx)),
		"│" + strings.Repeat(" ", legendW-2) + "│",
		makeLegendRow(innerLegendW, fmt.Sprintf("▲ %s", formatRate(txRate)), fmt.Sprintf("(%s)", formatBits(txRate))),
		makeLegendRow(innerLegendW, "▲ Top:", fmt.Sprintf("(%s)", formatBits(txPeak))),
		makeLegendRow(innerLegendW, "▲ Total:", formatBytes(totalTx)),
		botBorder,
	}

	// 2. Remaining width for Waveform Graph (scaleW=4, space=1, graphW, space=1, legendW=33 -> scaleW+1+graphW+1+legendW = innerW)
	scaleW := 4 // scale label e.g. "10K "
	graphW := innerW - legendW - scaleW - 2
	if graphW < 4 {
		graphW = 4
	}

	// 3. Build graph rows with Braille Canvas
	maxRx := int(rxPeak / 1024)
	if maxRx <= 0 {
		maxRx = 10
	}
	maxTx := int(txPeak / 1024)
	if maxTx <= 0 {
		maxTx = 10
	}

	brailleLines := renderBidirectionalBraille(graphW, innerH, rxHist, txHist, maxRx, maxTx)

	var contentLines []string
	legendStart := (innerH - len(legend)) / 2
	if legendStart < 0 {
		legendStart = 0
	}

	for r := 0; r < innerH; r++ {
		// Scale text on the left side (10K on top and 10K at bottom matching btop layout)
		scaleStr := "    "
		if r == 0 {
			scaleStr = fmt.Sprintf("%-4s", formatShortScale(rxPeak))
		} else if r == innerH-1 {
			scaleStr = fmt.Sprintf("%-4s", formatShortScale(txPeak))
		}

		graphStr := brailleLines[r]

		// Floating legend on the right side
		legStr := strings.Repeat(" ", legendW)
		if r >= legendStart && r < legendStart+len(legend) {
			legStr = legend[r-legendStart]
		}

		row := fmt.Sprintf("%s %s %s", scaleStr, graphStr, legStr)
		contentLines = append(contentLines, row)
	}

	tabs := []string{"³net", modeLabel + ": " + ipStr}
	rightTitle := fmt.Sprintf("sync auto zero ↢b %s n↣", ifaceName)
	return renderBtopBox(width, height, tabs, "", rightTitle, contentLines)
}

func formatCardTop(title string, width int) string {
	titleW := runewidth.StringWidth(title)
	rem := width - titleW - 5
	if rem < 0 {
		return "┌" + strings.Repeat("─", max(0, width-2)) + "┐"
	}
	return "┌─ " + title + " " + strings.Repeat("─", rem) + "┐"
}

func formatCardLine(content string, width int) string {
	inner := width - 4
	if inner < 0 {
		inner = 0
	}
	str := runewidth.Truncate(content, inner, "…")
	pad := inner - runewidth.StringWidth(str)
	if pad < 0 {
		pad = 0
	}
	return "│ " + str + strings.Repeat(" ", pad) + " │"
}

func formatCardBot(width int) string {
	return "└" + strings.Repeat("─", max(0, width-2)) + "┘"
}

// renderHotspotBox renders Box 2 [²hotspot] [stealth-nat] with dual-card architecture and visual routing pipeline.
func renderHotspotBox(width, height int, cfg Config, clientsCount int) string {
	innerW := width - 4
	innerH := height - 2
	tabs := []string{"²hotspot", "stealth-nat"}
	if cfg.VPNActive {
		tabs = append(tabs, "vpn")
	}

	if innerW < 30 || innerH < 4 {
		return renderBtopBox(width, height, tabs, "", "", nil)
	}

	hotspotStatus := "DISABLED"
	if cfg.HotspotActive {
		hotspotStatus = fmt.Sprintf("ACTIVE (%s)", cfg.SSID)
	}
	if cfg.VPNActive && cfg.VPNIface != "" {
		if cfg.HotspotActive {
			hotspotStatus += fmt.Sprintf(" · VPN: %s", cfg.VPNIface)
		} else {
			hotspotStatus = fmt.Sprintf("VPN: %s", cfg.VPNIface)
		}
	}

	wanTarget := cfg.WANIface
	if cfg.VPNActive && cfg.VPNIface != "" {
		wanTarget = cfg.VPNIface + " (vpn)"
	} else if wanTarget == "" {
		wanTarget = "WAN"
	}

	leaseMeter := makeSolidMeter(clientsCount, 41, 8)

	// If width is sufficient (>= 50 characters), use centered dual-card architecture
	if innerW >= 50 && innerH >= 7 {
		c1W := (innerW - 1) / 2
		c2W := innerW - 1 - c1W

		c1Lines := []string{
			formatCardTop("Wi-Fi AP (ap0)", c1W),
			formatCardLine(fmt.Sprintf("SSID: %s", cfg.SSID), c1W),
			formatCardLine("Ch 36 · 5 GHz (80 MHz)", c1W),
			formatCardLine("IP:   10.42.0.1/24", c1W),
			formatCardLine("MAC:  9e:12:21:07:03:5f", c1W),
			formatCardLine(fmt.Sprintf("DHCP: %s %d/41", leaseMeter, clientsCount), c1W),
			formatCardBot(c1W),
		}

		c2Lines := []string{
			formatCardTop("Stealth NAT Pipeline", c2W),
			formatCardLine(fmt.Sprintf("Flow: [ap0] ──► [%s]", wanTarget), c2W),
			formatCardLine(fmt.Sprintf("NAT:  MASQ -> %s", wanTarget), c2W),
			formatCardLine("DNS:  DNAT 53 -> Local", c2W),
			formatCardLine("Sys:  route_localnet=1", c2W),
			formatCardLine("Fwd:  ESTABLISHED ACCEPT", c2W),
			formatCardBot(c2W),
		}

		cardH := len(c1Lines)
		padTop := (innerH - cardH) / 2
		if padTop < 0 {
			padTop = 0
		}
		padBot := innerH - cardH - padTop
		if padBot < 0 {
			padBot = 0
		}

		totalCardsW := c1W + 1 + c2W
		leftPad := (innerW - totalCardsW) / 2
		if leftPad < 0 {
			leftPad = 0
		}
		rightPad := innerW - totalCardsW - leftPad
		if rightPad < 0 {
			rightPad = 0
		}

		var merged []string
		for r := 0; r < padTop; r++ {
			merged = append(merged, strings.Repeat(" ", innerW))
		}
		for r := 0; r < cardH; r++ {
			l1 := c1Lines[r]
			l2 := c2Lines[r]
			row := strings.Repeat(" ", leftPad) + l1 + " " + l2 + strings.Repeat(" ", rightPad)
			merged = append(merged, row)
		}
		for r := 0; r < padBot; r++ {
			merged = append(merged, strings.Repeat(" ", innerW))
		}

		return renderBtopBox(width, height, tabs, "", hotspotStatus, merged)
	}

	// Responsive tiered layout for constrained widths with vertical centering
	singleLines := []string{
		"── Wi-Fi AP [ap0: 10.42.0.1/24] ───────────────────────",
		fmt.Sprintf("SSID: %s  ·  5180 MHz (Ch 36 / 80 MHz)", cfg.SSID),
		"BSSID: 9e:12:21:07:03:5f  ·  Mode: 802.11ac Virtual AP",
		fmt.Sprintf("DHCP: %s %d/41 leases (10.42.0.10 - .50)", leaseMeter, clientsCount),
		"── Stealth NAT & Kernel Pipeline ───────────────────────",
		fmt.Sprintf("[ap0] ──► [DNAT :53] ──► [MASQUERADE] ──► [%s]", wanTarget),
		fmt.Sprintf("NAT: MASQUERADE -> %s  ·  DNS: Local DoH", wanTarget),
		"Martian: route_localnet=1  ·  Forward: ACCEPT",
	}
	sPadTop := (innerH - len(singleLines)) / 2
	if sPadTop < 0 {
		sPadTop = 0
	}
	var centeredSingle []string
	for r := 0; r < sPadTop; r++ {
		centeredSingle = append(centeredSingle, strings.Repeat(" ", innerW))
	}
	centeredSingle = append(centeredSingle, singleLines...)
	for len(centeredSingle) < innerH {
		centeredSingle = append(centeredSingle, strings.Repeat(" ", innerW))
	}
	return renderBtopBox(width, height, tabs, "", hotspotStatus, centeredSingle)
}

// View renders full-screen grid matching btop layout.
func (m Model) View() string {
	if m.quitting {
		if m.cfg.IsRemoteClient {
			return "Detached from routerd daemon. Daemon is still active in background.\n"
		}
		return "Shutting down routerd cleanly...\n"
	}

	totalW := m.width
	totalH := m.height
	if totalW < 100 {
		totalW = 100
	}
	if totalH < 28 {
		totalH = 28
	}

	uptime := time.Since(m.startTime).Round(time.Second)
	currTime := time.Now().Format("15:04:05")

	// ==========================================
	// 1. TOP BOX: [¹ebpf-tcx] [dpi-scrambler]
	// ==========================================
	topHeight := int(float64(totalH) * 0.34)
	if topHeight < 11 {
		topHeight = 11
	}

	topBoxStr := renderEbpfBox(totalW, topHeight, currTime, uptime, m.cfg.WANIface,
		m.clampRate, m.peakClampRate, m.totalClampCount, m.clampHistory,
		m.dnsRate, m.peakDNSRate, m.totalDNSCount, m.avgDNSLatency, m.dnsHistory,
		m.totalHelloCount)

	// ==========================================
	// 2. BOTTOM SECTION: TWO-COLUMN GRID
	// ==========================================
	bottomH := totalH - topHeight - 1
	leftW := int(float64(totalW) * 0.44)
	if leftW < 46 {
		leftW = 46
	}
	rightW := totalW - leftW

	// --- 2A. BOX 2: [²hotspot] [stealth-nat] (Top Left) ---
	box2H := int(float64(bottomH) * 0.50)
	box2Str := renderHotspotBox(leftW, box2H, m.cfg, len(m.clients))

	// --- 2B. BOX 3: [³net] (Bottom Left - Authentic btop Net Panel WAN / LAN) ---
	box3H := bottomH - box2H
	var netModeLabel string
	var netIface string
	var netIP string
	var netRxRate, netTxRate, netRxPeak, netTxPeak, netTotalRx, netTotalTx uint64
	var netRxHist, netTxHist []int

	if m.netModeIdx == 1 && m.cfg.HotspotActive {
		netModeLabel = "LAN"
		netIface = "ap0"
		netIP = "10.42.0.1"
		netRxRate = m.apRxRate
		netTxRate = m.apTxRate
		netRxPeak = m.apRxPeak
		netTxPeak = m.apTxPeak
		netTotalRx = m.totalApRx
		netTotalTx = m.totalApTx
		netRxHist = m.apRxHistory
		netTxHist = m.apTxHistory
	} else {
		netModeLabel = "WAN"
		netIface = m.cfg.WANIface
		netIP = m.cfg.WANIP
		if netIP == "" {
			netIP = "10.100.2.185"
		}
		netRxRate = m.rxRate
		netTxRate = m.txRate
		netRxPeak = m.rxPeak
		netTxPeak = m.txPeak
		netTotalRx = m.totalRx
		netTotalTx = m.totalTx
		netRxHist = m.rxHistory
		netTxHist = m.txHistory
	}

	box3Str := renderBtopNetBox(leftW, box3H, netModeLabel, netIface, netIP,
		netRxRate, netTxRate, netRxPeak, netTxPeak, netTotalRx, netTotalTx, netRxHist, netTxHist)

	leftCol := lipgloss.JoinVertical(lipgloss.Left, box2Str, box3Str)

	// --- 2C. BOX 4: [⁴stations & inspector] (Right Column, Full Height) ---
	box4Tabs := []string{"⁴stations & inspector"}
	box4Right := fmt.Sprintf("Clients: %d", len(m.clients))

	var box4Lines []string
	box4InnerW := rightW - 4

	// Client Table Header (adaptive to column width)
	hostW := 16
	if box4InnerW < 72 {
		hostW = 12
	}
	headerLine := fmt.Sprintf("  %-*s %-15s %-17s %-9s %-8s",
		hostW, "HOSTNAME", "IP ADDRESS", "MAC ADDRESS", "SIGNAL", "SPEED")
	box4Lines = append(box4Lines, headerLine)
	box4Lines = append(box4Lines, strings.Repeat("─", box4InnerW))

	// Client rows with arrow selection and scrolling
	maxClientRows := 5
	if len(m.clients) == 0 {
		box4Lines = append(box4Lines, "  No active wireless stations connected yet.")
	} else {
		if m.selectedIdx < 0 {
			m.selectedIdx = 0
		}
		if m.selectedIdx >= len(m.clients) {
			m.selectedIdx = len(m.clients) - 1
		}

		startClientIdx := 0
		if m.selectedIdx >= maxClientRows {
			startClientIdx = m.selectedIdx - maxClientRows + 1
		}
		endClientIdx := startClientIdx + maxClientRows
		if endClientIdx > len(m.clients) {
			endClientIdx = len(m.clients)
		}

		for i := startClientIdx; i < endClientIdx; i++ {
			c := m.clients[i]
			prefix := "  "
			suffix := ""
			isSelected := (i == m.selectedIdx && m.focusPane == 0)
			if isSelected {
				prefix = "▸ "
			}
			if m.filterClientIP == c.IP {
				suffix += " [FILTERED]"
			}

			line := fmt.Sprintf("%s%-*s %-15s %-17s %-9s %-8s%s",
				prefix,
				hostW,
				runewidth.Truncate(c.Hostname, hostW, "…"),
				c.IP,
				c.MAC,
				c.Signal,
				runewidth.Truncate(c.TxBitrate, 8, ""),
				suffix,
			)
			if isSelected {
				line = lipgloss.NewStyle().Bold(true).Reverse(true).Render(line)
			}
			box4Lines = append(box4Lines, line)
		}
	}

	// Separator to Live Feed
	box4Lines = append(box4Lines, "")

	// Filter logs if client IP filter is active
	var displayLogs []string
	if m.filterClientIP != "" {
		for _, l := range m.logs {
			if strings.Contains(l, m.filterClientIP) {
				displayLogs = append(displayLogs, l)
			}
		}
		if len(displayLogs) == 0 {
			displayLogs = append(displayLogs, fmt.Sprintf("No activity recorded yet for %s.", m.filterClientIP))
		}
	} else {
		displayLogs = m.logs
	}

	// Clamp selectedLogIdx to displayLogs boundaries
	if m.selectedLogIdx < 0 {
		m.selectedLogIdx = 0
	}
	if len(displayLogs) > 0 && m.selectedLogIdx >= len(displayLogs) {
		m.selectedLogIdx = len(displayLogs) - 1
	}

	totalLogs := len(displayLogs)
	currentLogNum := m.selectedLogIdx + 1
	if totalLogs == 0 {
		currentLogNum = 0
	}

	filterLabel := "LIVE SECURITY & DNS INSPECTION FEED"
	if m.focusPane == 1 {
		if m.filterClientIP != "" {
			filterLabel = fmt.Sprintf("LIVE FEED [FILTERED: %s · SELECTOR %d/%d] (↑/↓ select, Tab stations, Esc clear)", m.filterClientIP, currentLogNum, totalLogs)
		} else {
			filterLabel = fmt.Sprintf("LIVE FEED [ACTIVE · SELECTOR %d/%d] (↑/↓ select & scroll, End latest, Tab stations)", currentLogNum, totalLogs)
		}
	} else if m.filterClientIP != "" {
		filterLabel = fmt.Sprintf("LIVE FEED [FILTERED: %s · %d items] (Enter/Esc show all, Tab for live feed)", m.filterClientIP, totalLogs)
	}
	box4Lines = append(box4Lines, filterLabel)
	box4Lines = append(box4Lines, strings.Repeat("─", box4InnerW))

	// Live Logs with SELECTOR & SCROLLING VIEWPORT
	remainingLogRows := (box4H_rows(bottomH)) - len(box4Lines)
	if remainingLogRows > 0 {
		// Ensure m.selectedLogIdx is always visible in viewport
		if m.selectedLogIdx < m.logViewportStart {
			m.logViewportStart = m.selectedLogIdx
		}
		if m.selectedLogIdx >= m.logViewportStart+remainingLogRows {
			m.logViewportStart = m.selectedLogIdx - remainingLogRows + 1
		}
		maxStart := len(displayLogs) - remainingLogRows
		if maxStart < 0 {
			maxStart = 0
		}
		if m.logViewportStart > maxStart {
			m.logViewportStart = maxStart
		}
		if m.logViewportStart < 0 {
			m.logViewportStart = 0
		}

		startIdx := m.logViewportStart
		endIdx := startIdx + remainingLogRows
		if endIdx > len(displayLogs) {
			endIdx = len(displayLogs)
		}

		for i := startIdx; i < endIdx; i++ {
			line := displayLogs[i]
			isSelected := (i == m.selectedLogIdx && m.focusPane == 1)
			if isSelected {
				line = lipgloss.NewStyle().Bold(true).Reverse(true).Render("▸ " + line)
			} else {
				line = "  " + line
			}
			box4Lines = append(box4Lines, line)
		}
	}

	box4Str := renderBtopBox(rightW, bottomH, box4Tabs, "", box4Right, box4Lines)

	// Join Left and Right Columns
	bottomSection := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, box4Str)

	// ==========================================
	// 3. FOOTER LINE (btop style, centered)
	// ==========================================
	footerStyle := lipgloss.NewStyle().
		Faint(true).
		Width(totalW).
		Align(lipgloss.Center)
	quitAction := "quit"
	if m.cfg.IsRemoteClient {
		quitAction = "detach"
	}
	footerMsg := fmt.Sprintf("tab switch focus   ↑/↓ select station   ↵ filter   b/n switch WAN/LAN   c clear   q %s", quitAction)
	if m.focusPane == 1 {
		footerMsg = fmt.Sprintf("tab switch focus   ↑/↓ select log   PgUp/PgDn page   End latest   b/n switch WAN/LAN   c clear   q %s", quitAction)
	}
	footer := footerStyle.Render(footerMsg)

	return lipgloss.JoinVertical(lipgloss.Left, topBoxStr, bottomSection, footer)
}

func box4H_rows(totalH int) int {
	return totalH - 2
}
