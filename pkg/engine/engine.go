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

// XDPMode determines how XDP hooks are attached to the network interface.
type XDPMode int

const (
	// XDPModeAuto automatically detects the interface type: skips XDP if wireless
	// to prevent Wi-Fi card link flapping; attaches if loopback or wired ethernet.
	XDPModeAuto XDPMode = iota
	// XDPModeDisabled disables the XDP ingress hook entirely.
	XDPModeDisabled
	// XDPModeGeneric forces XDP to run in generic SKB mode.
	XDPModeGeneric
	// XDPModeDriver attempts to attach XDP at native driver level.
	XDPModeDriver
)

// Config holds initialization parameters for the Packet Engine.
type Config struct {
	InterfaceName string
	XDPMode       XDPMode
}

// StatsSnapshot is an immutable telemetry data record sampled from kernel eBPF maps.
type StatsSnapshot struct {
	ClampedPackets uint64
	ClientHellos   uint64
	DroppedPackets uint64
}

// StatsProvider is a consumer interface for components requiring telemetry metrics.
type StatsProvider interface {
	Stats() (StatsSnapshot, error)
}

// Engine manages the lifecycle of eBPF programs and network hooks in the Linux kernel.
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

// Start raises kernel rlimit, loads BPF programs, and attaches TCX and XDP hooks
// to the target interface. If an error occurs during setup, Start automatically
// rolls back and cleans up any hooks attached so far.
func Start(cfg Config) (*Engine, error) {
	// 1. Raise kernel locked memory limit for eBPF map and program allocation
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("failed to remove memlock rlimit: %w", err)
	}

	// 2. Resolve target interface
	var iface *net.Interface
	var err error
	if cfg.InterfaceName == "" {
		iface, err = netutil.GetDefaultInterface()
		if err != nil {
			return nil, fmt.Errorf("failed to auto-detect network interface: %w", err)
		}
	} else {
		iface, err = net.InterfaceByName(cfg.InterfaceName)
		if err != nil {
			return nil, fmt.Errorf("network interface %s not found: %w", cfg.InterfaceName, err)
		}
	}

	eng := &Engine{
		cfg:   cfg,
		iface: iface,
	}

	// 3. Load eBPF objects into kernel
	if err := bpf.LoadBpfObjects(&eng.objs, nil); err != nil {
		return nil, fmt.Errorf("failed to load eBPF objects into kernel: %w", err)
	}

	// Rollback helper if attachment fails midway
	rollback := func(cause error) (*Engine, error) {
		_ = eng.Close()
		return nil, cause
	}

	// 4. Attach TCX Egress (DPI Hunter)
	lTcEgress, err := link.AttachTCX(link.TCXOptions{
		Program:   eng.objs.TcEgressFunc,
		Attach:    ebpf.AttachTCXEgress,
		Interface: iface.Index,
	})
	if err != nil {
		return rollback(fmt.Errorf("failed to attach TCX Egress to %s: %w", iface.Name, err))
	}
	eng.tcxEgress = lTcEgress

	// 5. Attach TCX Ingress (TCP Window Clamper)
	lTcIngress, err := link.AttachTCX(link.TCXOptions{
		Program:   eng.objs.TcIngressFunc,
		Attach:    ebpf.AttachTCXIngress,
		Interface: iface.Index,
	})
	if err != nil {
		return rollback(fmt.Errorf("failed to attach TCX Ingress to %s: %w", iface.Name, err))
	}
	eng.tcxIngress = lTcIngress

	// 6. Attach XDP Ingress based on policy
	shouldAttachXDP := false
	var xdpFlags link.XDPAttachFlags

	switch cfg.XDPMode {
	case XDPModeAuto:
		if iface.Name == "lo" {
			shouldAttachXDP = true
			xdpFlags = link.XDPGenericMode
		} else if !netutil.IsWireless(iface.Name) {
			// Wired Ethernet: safe to attach generic XDP
			shouldAttachXDP = true
			xdpFlags = link.XDPGenericMode
		}
		// Physical wireless: default to skip to avoid driver link-flap
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
				return rollback(fmt.Errorf("failed to attach XDP to %s: %w", iface.Name, err))
			}
		} else {
			eng.xdpLink = lXdp
		}
	}

	return eng, nil
}

// InterfaceName returns the name of the network interface managed by Engine.
func (e *Engine) InterfaceName() string {
	if e.iface != nil {
		return e.iface.Name
	}
	return ""
}

// Interface returns the active *net.Interface pointer.
func (e *Engine) Interface() *net.Interface {
	return e.iface
}

// Stats reads the latest counter values from kernel BPF maps.
func (e *Engine) Stats() (StatsSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return StatsSnapshot{}, errors.New("engine is closed")
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

// Close detaches all hooks (TCX and XDP) and frees eBPF objects in kernel.
// This method is idempotent.
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
			errs = append(errs, fmt.Errorf("failed to close xdp link: %w", err))
		}
		e.xdpLink = nil
	}

	if e.tcxIngress != nil {
		if err := e.tcxIngress.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close tcx ingress: %w", err))
		}
		e.tcxIngress = nil
	}

	if e.tcxEgress != nil {
		if err := e.tcxEgress.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close tcx egress: %w", err))
		}
		e.tcxEgress = nil
	}

	if err := e.objs.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close bpf objects: %w", err))
	}

	return errors.Join(errs...)
}
