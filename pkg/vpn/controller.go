package vpn

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const wireguardTable = "51820"

// Config holds options for the VPN controller.
type Config struct {
	ProfilePath string
	RunDir      string
	APInterface string
}

// Controller manages the WireGuard VPN lifecycle, asymmetric routing sysctl
// adjustments, and policy routing rules.
type Controller struct {
	cfg         Config
	ifaceName   string
	runtimeConf string
	active      bool
	origRPAP    string
	origRPAll   string
	mu          sync.Mutex
}

// NewController creates a new VPN controller instance.
func NewController(cfg Config) *Controller {
	if cfg.ProfilePath == "" {
		cfg.ProfilePath = "/etc/routerd/vpn.conf"
	}
	if cfg.RunDir == "" {
		cfg.RunDir = "/run/routerd"
	}
	if cfg.APInterface == "" {
		cfg.APInterface = "ap0"
	}
	return &Controller{
		cfg: cfg,
	}
}

// IsActive returns whether the WireGuard VPN tunnel is currently up.
func (c *Controller) IsActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

// InterfaceName returns the name of the active VPN interface (e.g. 'wg0' or 'vpn').
func (c *Controller) InterfaceName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ifaceName
}

// Start brings up the WireGuard VPN tunnel and applies necessary policy routing.
func (c *Controller) Start() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.active {
		return c.ifaceName, nil
	}

	if !IsConfigured(c.cfg.ProfilePath) {
		return "", fmt.Errorf("VPN configuration %s is missing or has unconfigured keys", c.cfg.ProfilePath)
	}

	runtimeConf, err := PrepareRuntimeConfig(c.cfg.ProfilePath, c.cfg.RunDir)
	if err != nil {
		return "", fmt.Errorf("failed to prepare runtime VPN config: %w", err)
	}

	iface := DeriveInterfaceName(runtimeConf, "wg0")

	// Tear down any stale interface from an unclean termination or previous run
	_ = exec.Command("wg-quick", "down", runtimeConf).Run()
	_ = exec.Command("ip", "link", "del", "dev", iface).Run()

	log.Printf("VPN: Starting WireGuard tunnel on interface [%s] using [%s]...", iface, runtimeConf)
	cmd := exec.Command("wg-quick", "up", runtimeConf)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(runtimeConf)
		return "", fmt.Errorf("wg-quick up failed: %s (err: %w)", strings.TrimSpace(string(out)), err)
	}

	c.ifaceName = iface
	c.runtimeConf = runtimeConf
	c.active = true

	// Apply policy routing and disable strict reverse-path filtering
	c.setupPolicyRouting()

	log.Printf("SUCCESS: WireGuard VPN ACTIVE on interface [%s]!", iface)
	return iface, nil
}

// Stop cleanly terminates the WireGuard VPN tunnel and reverts policy routing.
func (c *Controller) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.active {
		return nil
	}

	log.Printf("VPN: Stopping WireGuard tunnel [%s]...", c.ifaceName)

	// 1. Revert policy routing and restore rp_filter
	c.cleanupPolicyRouting()

	// 2. Bring down WireGuard interface
	if c.runtimeConf != "" {
		_ = exec.Command("wg-quick", "down", c.runtimeConf).Run()
		_ = os.Remove(c.runtimeConf)
	}
	if c.ifaceName != "" {
		_ = exec.Command("ip", "link", "del", "dev", c.ifaceName).Run()
	}

	c.active = false
	c.ifaceName = ""
	c.runtimeConf = ""

	log.Println("SUCCESS: WireGuard VPN cleaned up safely!")
	return nil
}

func (c *Controller) setupPolicyRouting() {
	ap := c.cfg.APInterface
	apExists := false
	if ap != "" {
		if _, err := os.Stat(fmt.Sprintf("/proc/sys/net/ipv4/conf/%s", ap)); err == nil {
			apExists = true
			c.origRPAP = readSysctl(fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", ap))
			writeSysctl(fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", ap), "0")
			_ = exec.Command("ip", "rule", "add", "iif", ap, "table", wireguardTable).Run()
		}
	}

	// Always ensure global all/rp_filter allows asymmetric routing
	c.origRPAll = readSysctl("/proc/sys/net/ipv4/conf/all/rp_filter")
	writeSysctl("/proc/sys/net/ipv4/conf/all/rp_filter", "0")

	if c.ifaceName != "" {
		// Masquerade outbound traffic over VPN tunnel
		_ = exec.Command("iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-o", c.ifaceName, "-j", "MASQUERADE").Run()
		// Clamp TCP MSS to PMTU to avoid MTU blackholes across WireGuard tunnel
		_ = exec.Command("iptables", "-t", "mangle", "-I", "FORWARD", "1", "-o", c.ifaceName, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu").Run()
		_ = exec.Command("iptables", "-t", "mangle", "-I", "FORWARD", "1", "-i", c.ifaceName, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu").Run()

		if apExists {
			// Forwarding rules between AP and VPN interface
			_ = exec.Command("iptables", "-I", "FORWARD", "1", "-i", ap, "-o", c.ifaceName, "-j", "ACCEPT").Run()
			_ = exec.Command("iptables", "-I", "FORWARD", "1", "-i", c.ifaceName, "-o", ap, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT").Run()
		}
	}
}

func (c *Controller) cleanupPolicyRouting() {
	ap := c.cfg.APInterface
	if ap != "" {
		_ = exec.Command("ip", "rule", "del", "iif", ap, "table", wireguardTable).Run()
		if c.origRPAP != "" {
			writeSysctl(fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", ap), c.origRPAP)
		}
		if c.ifaceName != "" {
			_ = exec.Command("iptables", "-D", "FORWARD", "-i", ap, "-o", c.ifaceName, "-j", "ACCEPT").Run()
			_ = exec.Command("iptables", "-D", "FORWARD", "-i", c.ifaceName, "-o", ap, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT").Run()
		}
	}
	if c.origRPAll != "" {
		writeSysctl("/proc/sys/net/ipv4/conf/all/rp_filter", c.origRPAll)
	}
	if c.ifaceName != "" {
		_ = exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING", "-o", c.ifaceName, "-j", "MASQUERADE").Run()
		_ = exec.Command("iptables", "-t", "mangle", "-D", "FORWARD", "-o", c.ifaceName, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu").Run()
		_ = exec.Command("iptables", "-t", "mangle", "-D", "FORWARD", "-i", c.ifaceName, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu").Run()
	}
}

func readSysctl(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeSysctl(path, val string) {
	_ = os.WriteFile(path, []byte(val), 0644)
}
