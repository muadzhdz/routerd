package dashboard

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BandwidthSnapshot holds instantaneous TX/RX rates for an interface and
// per-client breakdown derived from the iw station dump.
type BandwidthSnapshot struct {
	TotalTxBps   int64         `json:"total_tx_bps"`
	TotalRxBps   int64         `json:"total_rx_bps"`
	TotalTxHuman string        `json:"total_tx_human"`
	TotalRxHuman string        `json:"total_rx_human"`
	Clients      []ClientBW    `json:"clients"`
	History      []BWHistPoint `json:"history"` // last 60 points (1 per second)
}

// ClientBW holds the bandwidth stats for a single client.
type ClientBW struct {
	MAC     string `json:"mac"`
	TxBytes int64  `json:"tx_bytes"`
	RxBytes int64  `json:"rx_bytes"`
	TxBps   int64  `json:"tx_bps"`
	RxBps   int64  `json:"rx_bps"`
	TxHuman string `json:"tx_human"`
	RxHuman string `json:"rx_human"`
}

// BWHistPoint is one sample in the 60-second rolling bandwidth history.
type BWHistPoint struct {
	Time  int64 `json:"t"`
	TxBps int64 `json:"tx"`
	RxBps int64 `json:"rx"`
}

type bwTracker struct {
	mu      sync.Mutex
	prev    map[string]ifaceSample
	history []BWHistPoint
}

type ifaceSample struct {
	rxBytes int64
	txBytes int64
	ts      time.Time
}

var tracker = &bwTracker{
	prev: make(map[string]ifaceSample),
}

// CollectBandwidth returns a snapshot of current bandwidth for the AP
// interface and all connected clients.
func CollectBandwidth(ap string, clients []ClientInfo) *BandwidthSnapshot {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	now := time.Now()
	snap := &BandwidthSnapshot{}

	// AP interface total from /proc/net/dev.
	apRx, apTx := readProcNetDev(ap)
	if prev, ok := tracker.prev[ap]; ok {
		dt := now.Sub(prev.ts).Seconds()
		if dt > 0 {
			snap.TotalRxBps = int64(float64(apRx-prev.rxBytes) / dt)
			snap.TotalTxBps = int64(float64(apTx-prev.txBytes) / dt)
			if snap.TotalRxBps < 0 {
				snap.TotalRxBps = 0
			}
			if snap.TotalTxBps < 0 {
				snap.TotalTxBps = 0
			}
		}
	}
	tracker.prev[ap] = ifaceSample{rxBytes: apRx, txBytes: apTx, ts: now}
	snap.TotalTxHuman = humanBytes(snap.TotalTxBps)
	snap.TotalRxHuman = humanBytes(snap.TotalRxBps)

	// Per-client from iw station dump.
	for _, c := range clients {
		staTx, staRx := readStationCounters(ap, c.MAC)
		key := "sta:" + c.MAC
		cbw := ClientBW{MAC: c.MAC, TxBytes: staTx, RxBytes: staRx}
		if prev, ok := tracker.prev[key]; ok {
			dt := now.Sub(prev.ts).Seconds()
			if dt > 0 {
				cbw.TxBps = int64(float64(staTx-prev.txBytes) / dt)
				cbw.RxBps = int64(float64(staRx-prev.rxBytes) / dt)
				if cbw.TxBps < 0 {
					cbw.TxBps = 0
				}
				if cbw.RxBps < 0 {
					cbw.RxBps = 0
				}
			}
		}
		tracker.prev[key] = ifaceSample{rxBytes: staRx, txBytes: staTx, ts: now}
		cbw.TxHuman = humanBytes(cbw.TxBps)
		cbw.RxHuman = humanBytes(cbw.RxBps)
		snap.Clients = append(snap.Clients, cbw)
	}
	if snap.Clients == nil {
		snap.Clients = []ClientBW{}
	}

	// Rolling 60-second history.
	tracker.history = append(tracker.history, BWHistPoint{
		Time:  now.Unix(),
		TxBps: snap.TotalTxBps,
		RxBps: snap.TotalRxBps,
	})
	if len(tracker.history) > 60 {
		tracker.history = tracker.history[len(tracker.history)-60:]
	}
	snap.History = make([]BWHistPoint, len(tracker.history))
	copy(snap.History, tracker.history)

	return snap
}

func readProcNetDev(iface string) (rx, tx int64) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, iface+":") {
			continue
		}
		line = strings.TrimPrefix(line, iface+":")
		fields := strings.Fields(line)
		if len(fields) >= 9 {
			rx, _ = strconv.ParseInt(fields[0], 10, 64)
			tx, _ = strconv.ParseInt(fields[8], 10, 64)
		}
		return rx, tx
	}
	return 0, 0
}

func readStationCounters(ap, mac string) (tx, rx int64) {
	out, err := runCmdOut("iw", "dev", ap, "station", "get", mac)
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "rx bytes:") {
			rx, _ = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "rx bytes:")), 10, 64)
		}
		if strings.HasPrefix(line, "tx bytes:") {
			tx, _ = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "tx bytes:")), 10, 64)
		}
	}
	return tx, rx
}

func humanBytes(bps int64) string {
	switch {
	case bps >= 1_000_000:
		return strconv.FormatFloat(float64(bps)/1_000_000, 'f', 1, 64) + " Mbps"
	case bps >= 1_000:
		return strconv.FormatFloat(float64(bps)/1_000, 'f', 1, 64) + " Kbps"
	default:
		return strconv.FormatInt(bps, 10) + " bps"
	}
}
