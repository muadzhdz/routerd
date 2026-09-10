package engine

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/muadzhdz/routerd/pkg/engine/bpf"
	"github.com/muadzhdz/routerd/pkg/netutil"
)

// XDPMode menentukan bagaimana XDP hook dipasang ke interface jaringan.
type XDPMode int

const (
	// XDPModeAuto otomatis mendeteksi tipe interface: jika wireless, lewati XDP
	// untuk menghindari link flap kartu Wi-Fi; pasang jika loopback atau kabel.
	XDPModeAuto XDPMode = iota
	// XDPModeDisabled mematikan XDP ingress hook sama sekali.
	XDPModeDisabled
	// XDPModeGeneric memaksa XDP berjalan dalam mode generic SKB.
	XDPModeGeneric
	// XDPModeDriver mencoba memasang XDP di level native driver network.
	XDPModeDriver
)

// Config menyimpan konfigurasi inisialisasi Packet Engine.
type Config struct {
	InterfaceName string
	XDPMode       XDPMode
}

// StatsSnapshot adalah data metrik immutable hasil sampling dari kernel eBPF maps.
type StatsSnapshot struct {
	ClampedPackets uint64
	ClientHellos   uint64
	DroppedPackets uint64
}

// StatsProvider adalah consumer interface untuk komponen yang membutuhkan metrik telemetry.
type StatsProvider interface {
	Stats() (StatsSnapshot, error)
}

// Engine mengelola lifecycle program eBPF dan hook jaringan di kernel Linux.
type Engine struct {
	cfg        Config
	iface      *net.Interface
	objs       bpf.BpfObjects
	tcxEgress  link.Link
	tcxIngress link.Link
	xdpLink    link.Link
	mu         sync.Mutex
	closed     bool
}

// Start menginisialisasi rlimit kernel, memuat program BPF, dan memasang hook
// TCX dan XDP ke interface target. Jika terjadi error di tengah proses,
// Start secara otomatis membatalkan dan membersihkan semua hook yang sempat terpasang.
func Start(cfg Config) (*Engine, error) {
	// 1. Naikkan batas locked memory kernel untuk alokasi map dan program eBPF
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("gagal me-remove memlock rlimit: %w", err)
	}

	// 2. Tentukan interface target
	var iface *net.Interface
	var err error
	if cfg.InterfaceName == "" {
		iface, err = netutil.GetDefaultInterface()
		if err != nil {
			return nil, fmt.Errorf("gagal auto-detect interface: %w", err)
		}
	} else {
		iface, err = net.InterfaceByName(cfg.InterfaceName)
		if err != nil {
			return nil, fmt.Errorf("interface %s tidak ditemukan: %w", cfg.InterfaceName, err)
		}
	}

	eng := &Engine{
		cfg:   cfg,
		iface: iface,
	}

	// 3. Load eBPF objects ke kernel
	if err := bpf.LoadBpfObjects(&eng.objs, nil); err != nil {
		return nil, fmt.Errorf("gagal me-load eBPF objects ke kernel: %w", err)
	}

	// Helper rollback jika attachment gagal di tengah jalan
	rollback := func(cause error) (*Engine, error) {
		_ = eng.Close()
		return nil, cause
	}

	// 4. Pasang TCX Egress (DPI Hunter)
	lTcEgress, err := link.AttachTCX(link.TCXOptions{
		Program:   eng.objs.TcEgressFunc,
		Attach:    ebpf.AttachTCXEgress,
		Interface: iface.Index,
	})
	if err != nil {
		return rollback(fmt.Errorf("gagal attach TCX Egress ke %s: %w", iface.Name, err))
	}
	eng.tcxEgress = lTcEgress

	// 5. Pasang TCX Ingress (TCP Window Clamper)
	lTcIngress, err := link.AttachTCX(link.TCXOptions{
		Program:   eng.objs.TcIngressFunc,
		Attach:    ebpf.AttachTCXIngress,
		Interface: iface.Index,
	})
	if err != nil {
		return rollback(fmt.Errorf("gagal attach TCX Ingress ke %s: %w", iface.Name, err))
	}
	eng.tcxIngress = lTcIngress

	// 6. Pasang XDP Ingress berdasarkan policy
	shouldAttachXDP := false
	var xdpFlags link.XDPAttachFlags

	switch cfg.XDPMode {
	case XDPModeAuto:
		if iface.Name == "lo" {
			shouldAttachXDP = true
			xdpFlags = link.XDPGenericMode
		} else if !netutil.IsWireless(iface.Name) {
			// Kabel Ethernet: aman pasang generic XDP
			shouldAttachXDP = true
			xdpFlags = link.XDPGenericMode
		}
		// Jika wireless fisik, default lewati agar tidak link flap
	case XDPModeGeneric:
		shouldAttachXDP = true
		xdpFlags = link.XDPGenericMode
	case XDPModeDriver:
		shouldAttachXDP = true
		xdpFlags = link.XDPDriverMode
	case XDPModeDisabled:
		shouldAttachXDP = false
	}

	if shouldAttachXDP {
		lXdp, err := link.AttachXDP(link.XDPOptions{
			Program:   eng.objs.XdpRouterFunc,
			Interface: iface.Index,
			Flags:     xdpFlags,
		})
		if err != nil {
			if cfg.XDPMode != XDPModeAuto {
				return rollback(fmt.Errorf("gagal attach XDP ke %s: %w", iface.Name, err))
			}
		} else {
			eng.xdpLink = lXdp
		}
	}

	return eng, nil
}

// InterfaceName mengembalikan nama interface jaringan yang dikontrol oleh Engine.
func (e *Engine) InterfaceName() string {
	if e.iface != nil {
		return e.iface.Name
	}
	return ""
}

// Interface mengembalikan pointer *net.Interface aktif.
func (e *Engine) Interface() *net.Interface {
	return e.iface
}

// Stats membaca nilai penghitung terbaru dari BPF maps di kernel.
func (e *Engine) Stats() (StatsSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return StatsSnapshot{}, errors.New("engine sudah ditutup")
	}

	var snap StatsSnapshot
	const key = uint32(0)

	if e.objs.SynackClampCount != nil {
		_ = e.objs.SynackClampCount.Lookup(key, &snap.ClampedPackets)
	}
	if e.objs.ClientHelloCount != nil {
		_ = e.objs.ClientHelloCount.Lookup(key, &snap.ClientHellos)
	}
	if e.objs.TcpDropCount != nil {
		_ = e.objs.TcpDropCount.Lookup(key, &snap.DroppedPackets)
	}

	return snap, nil
}

// Close melepaskan seluruh link hook (TCX dan XDP) dan membebaskan eBPF objects di kernel.
// Method ini bersifat idempotent.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}
	e.closed = true

	var errs []error

	if e.xdpLink != nil {
		if err := e.xdpLink.Close(); err != nil {
			errs = append(errs, fmt.Errorf("gagal close xdp link: %w", err))
		}
		e.xdpLink = nil
	}

	if e.tcxIngress != nil {
		if err := e.tcxIngress.Close(); err != nil {
			errs = append(errs, fmt.Errorf("gagal close tcx ingress: %w", err))
		}
		e.tcxIngress = nil
	}

	if e.tcxEgress != nil {
		if err := e.tcxEgress.Close(); err != nil {
			errs = append(errs, fmt.Errorf("gagal close tcx egress: %w", err))
		}
		e.tcxEgress = nil
	}

	if err := e.objs.Close(); err != nil {
		errs = append(errs, fmt.Errorf("gagal close bpf objects: %w", err))
	}

	return errors.Join(errs...)
}
