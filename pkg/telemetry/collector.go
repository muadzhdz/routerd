package telemetry

import (
	"sync"

	"github.com/muadzhdz/routerd/pkg/engine"
	"github.com/muadzhdz/routerd/pkg/hotspot"
)

// HistorySize adalah jumlah titik data dalam ring buffer grafik Braille
const HistorySize = 48

// BandwidthStats menyimpan ringkasan statistik bandwidth suatu interface
type BandwidthStats struct {
	RxBytes   uint64
	TxBytes   uint64
	RxRate    uint64 // bytes per second
	TxRate    uint64 // bytes per second
	RxPeak    uint64 // bytes per second
	TxPeak    uint64 // bytes per second
	RxHistory []int  // dalam satuan KB/s untuk visualisasi grafik
	TxHistory []int  // dalam satuan KB/s untuk visualisasi grafik
}

// Snapshot merepresentasikan data telemetry atomik hasil sampling pada satu interval waktu
type Snapshot struct {
	WAN            BandwidthStats
	LAN            BandwidthStats
	ClampedPackets uint64
	ClampRate      int
	PeakClampRate  int
	ClientHellos   uint64
	ClampHistory   []int
	Clients        []hotspot.ConnectedClient
}

// Config membawa konfigurasi untuk inisialisasi Telemetry Collector
type Config struct {
	WANIface      string
	LANIface      string
	HotspotActive bool
	StatsProvider engine.StatsProvider
	ClientFetcher func() ([]hotspot.ConnectedClient, error)
	NetDevReader  func(iface string) (rx, tx uint64)
}

// Collector mengelola sampling rate, tracking nilai puncak, dan ring buffer riwayat
type Collector struct {
	cfg Config
	mu  sync.Mutex

	// State WAN
	prevRxBytes uint64
	prevTxBytes uint64
	wanRxPeak   uint64
	wanTxPeak   uint64
	wanRxHist   []int
	wanTxHist   []int

	// State LAN (Hotspot)
	prevLanRx uint64
	prevLanTx uint64
	lanRxPeak uint64
	lanTxPeak uint64
	lanRxHist []int
	lanTxHist []int

	// State eBPF
	prevClampCount uint64
	peakClampRate  int
	clampHist      []int

	// State Cache Clients
	clients []hotspot.ConnectedClient
}

// NewCollector membuat instance Telemetry Collector baru
func NewCollector(cfg Config) *Collector {
	if cfg.LANIface == "" {
		cfg.LANIface = "ap0"
	}
	if cfg.NetDevReader == nil {
		cfg.NetDevReader = ReadNetDev
	}
	if cfg.ClientFetcher == nil {
		cfg.ClientFetcher = hotspot.GetConnectedClients
	}

	c := &Collector{
		cfg:       cfg,
		wanRxHist: make([]int, HistorySize),
		wanTxHist: make([]int, HistorySize),
		lanRxHist: make([]int, HistorySize),
		lanTxHist: make([]int, HistorySize),
		clampHist: make([]int, HistorySize),
	}

	c.prevRxBytes, c.prevTxBytes = c.cfg.NetDevReader(c.cfg.WANIface)
	if c.cfg.HotspotActive {
		c.prevLanRx, c.prevLanTx = c.cfg.NetDevReader(c.cfg.LANIface)
	}
	if c.cfg.StatsProvider != nil {
		if snap, err := c.cfg.StatsProvider.Stats(); err == nil {
			c.prevClampCount = snap.ClampedPackets
		}
	}

	return c
}

// Sample mengambil data terbaru, menghitung delta rates, dan menggeser riwayat sparkline
func (c *Collector) Sample() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	var snap Snapshot

	// 1. Sampling eBPF Engine Stats
	var currentClamp uint64
	var currentHello uint64
	if c.cfg.StatsProvider != nil {
		if s, err := c.cfg.StatsProvider.Stats(); err == nil {
			currentClamp = s.ClampedPackets
			currentHello = s.ClientHellos
		}
	}

	var clampRate int
	if c.prevClampCount > 0 && currentClamp >= c.prevClampCount {
		clampRate = int(currentClamp - c.prevClampCount)
	}
	if clampRate > c.peakClampRate {
		c.peakClampRate = clampRate
	}
	c.prevClampCount = currentClamp

	c.clampHist = append(c.clampHist[1:], clampRate)

	snap.ClampedPackets = currentClamp
	snap.ClientHellos = currentHello
	snap.ClampRate = clampRate
	snap.PeakClampRate = c.peakClampRate
	snap.ClampHistory = make([]int, HistorySize)
	copy(snap.ClampHistory, c.clampHist)

	// 2. Sampling WAN Bandwidth
	curWanRx, curWanTx := c.cfg.NetDevReader(c.cfg.WANIface)
	var wanRxRate, wanTxRate uint64
	if c.prevRxBytes > 0 && curWanRx >= c.prevRxBytes {
		wanRxRate = curWanRx - c.prevRxBytes
	}
	if c.prevTxBytes > 0 && curWanTx >= c.prevTxBytes {
		wanTxRate = curWanTx - c.prevTxBytes
	}
	if wanRxRate > c.wanRxPeak {
		c.wanRxPeak = wanRxRate
	}
	if wanTxRate > c.wanTxPeak {
		c.wanTxPeak = wanTxRate
	}
	c.prevRxBytes = curWanRx
	c.prevTxBytes = curWanTx

	c.wanRxHist = append(c.wanRxHist[1:], int(wanRxRate/1024))
	c.wanTxHist = append(c.wanTxHist[1:], int(wanTxRate/1024))

	snap.WAN = BandwidthStats{
		RxBytes:   curWanRx,
		TxBytes:   curWanTx,
		RxRate:    wanRxRate,
		TxRate:    wanTxRate,
		RxPeak:    c.wanRxPeak,
		TxPeak:    c.wanTxPeak,
		RxHistory: make([]int, HistorySize),
		TxHistory: make([]int, HistorySize),
	}
	copy(snap.WAN.RxHistory, c.wanRxHist)
	copy(snap.WAN.TxHistory, c.wanTxHist)

	// 3. Sampling LAN Bandwidth (jika Hotspot aktif)
	if c.cfg.HotspotActive {
		curLanRx, curLanTx := c.cfg.NetDevReader(c.cfg.LANIface)
		var lanRxRate, lanTxRate uint64
		if c.prevLanRx > 0 && curLanRx >= c.prevLanRx {
			lanRxRate = curLanRx - c.prevLanRx
		}
		if c.prevLanTx > 0 && curLanTx >= c.prevLanTx {
			lanTxRate = curLanTx - c.prevLanTx
		}
		if lanRxRate > c.lanRxPeak {
			c.lanRxPeak = lanRxRate
		}
		if lanTxRate > c.lanTxPeak {
			c.lanTxPeak = lanTxRate
		}
		c.prevLanRx = curLanRx
		c.prevLanTx = curLanTx

		c.lanRxHist = append(c.lanRxHist[1:], int(lanRxRate/1024))
		c.lanTxHist = append(c.lanTxHist[1:], int(lanTxRate/1024))

		snap.LAN = BandwidthStats{
			RxBytes:   curLanRx,
			TxBytes:   curLanTx,
			RxRate:    lanRxRate,
			TxRate:    lanTxRate,
			RxPeak:    c.lanRxPeak,
			TxPeak:    c.lanTxPeak,
			RxHistory: make([]int, HistorySize),
			TxHistory: make([]int, HistorySize),
		}
		copy(snap.LAN.RxHistory, c.lanRxHist)
		copy(snap.LAN.TxHistory, c.lanTxHist)

		if clients, err := c.cfg.ClientFetcher(); err == nil {
			c.clients = clients
		}
		snap.Clients = make([]hotspot.ConnectedClient, len(c.clients))
		copy(snap.Clients, c.clients)
	}

	return snap
}
