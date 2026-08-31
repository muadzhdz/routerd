package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// iptables command names.
const (
	iptablesCmd  = "iptables"
	ip6tablesCmd = "ip6tables"
)

// isNFTables returns true if iptables is backed by nf_tables (xtables-nft-multi).
// iptables-nft does not support the -w (wait/lock) flag — passing it causes
// exit status 4 on certain table/chain operations.
//
// The value is computed once on first use (lazy) rather than at init time so
// that tests can swap defaultRunner before the detection query runs.
var (
	_isNFT     bool
	_isNFTOnce sync.Once
)

func isNFT() bool {
	_isNFTOnce.Do(func() {
		out, _ := runCmd("iptables", "--version")
		_isNFT = strings.Contains(out, "nf_tables") || strings.Contains(out, "nft")
	})
	return _isNFT
}

// iptArgs builds an iptables argument list, stripping -w on nft-based systems.
func iptArgs(args ...string) []string {
	if !isNFT() {
		return args
	}
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != "-w" {
			out = append(out, a)
		}
	}
	return out
}

// runIpt runs iptables with the given args, auto-stripping -w on nft systems.
func runIpt(args ...string) (string, error) {
	return runCmd(iptablesCmd, iptArgs(args...)...)
}

// runIp6t runs ip6tables with the given args, auto-stripping -w on nft systems.
func runIp6t(args ...string) (string, error) {
	return runCmd(ip6tablesCmd, iptArgs(args...)...)
}

// routerd iptables chain names — all prefixed to allow clean teardown.
const (
	natPostChain = "ROUTERD_POST"
	natPreChain  = "ROUTERD_PRE"
	fwdChain     = "ROUTERD_FWD"
	inputChain   = "ROUTERD_IN"
	mangleChain  = "ROUTERD_MANGLE"
)

// sysctl paths.
const (
	forwardSys     = "/proc/sys/net/ipv4/ip_forward"
	rpFilterAllSys = "/proc/sys/net/ipv4/conf/all/rp_filter"
)

// WireGuard policy routing table used by wg-quick.
const wireguardTable = "51820"

// Tor transparent proxy ports.
const (
	torDNSPort = "5353"
	torTCPPort = "9040"
)

// isAlreadyExists reports whether an iptables command failed only because the
// chain or rule already exists — a safe-to-ignore condition.
func isAlreadyExists(output string) bool {
	return strings.Contains(output, "already exists") ||
		strings.Contains(output, "Already exists")
}

// --- sysctl helpers ----------------------------------------------------------

func readSysctl(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "0"
	}
	return strings.TrimSpace(string(data))
}

func writeSysctl(path, value string) error {
	return os.WriteFile(path, []byte(value), 0644)
}

func readIPForward() string    { return readSysctl(forwardSys) }
func setIPForward(v int) error { return writeSysctl(forwardSys, strconv.Itoa(v)) }

func rpFilterPath(iface string) string {
	return fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", iface)
}

// --- IPv6 leak protection ----------------------------------------------------

func setupIPv6LeakProtection(ap string) {
	sysctlPath := fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", ap)
	if err := writeSysctl(sysctlPath, "1"); err != nil {
		logWarn("cannot disable IPv6 on %s via sysctl: %v", ap, err)
	} else {
		logInfo("IPv6 disabled on %s interface (sysctl)", ap)
	}
	_, _ = runIp6t("-w", "-I", "INPUT", "-i", ap, "-j", "DROP")
	_, _ = runIp6t("-w", "-I", "FORWARD", "-i", ap, "-j", "DROP")
	_, _ = runIp6t("-w", "-I", "FORWARD", "-o", ap, "-j", "DROP")
	logInfo("IPv6 leak protection rules active (ip6tables DROP on %s)", ap)
}

func cleanupIPv6LeakProtection(ap string) {
	if ap == "" {
		return
	}
	sysctlPath := fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", ap)
	_ = writeSysctl(sysctlPath, "0")
	_, _ = runIp6t("-w", "-D", "INPUT", "-i", ap, "-j", "DROP")
	_, _ = runIp6t("-w", "-D", "FORWARD", "-i", ap, "-j", "DROP")
	_, _ = runIp6t("-w", "-D", "FORWARD", "-o", ap, "-j", "DROP")
}

// --- Rate limiting -----------------------------------------------------------

func setupRateLimit(ap string, mbps int) {
	if mbps <= 0 {
		return
	}
	out, err := runCmd("tc", "qdisc", "add", "dev", ap, "root", "tbf",
		"rate", fmt.Sprintf("%dmbit", mbps), "burst", "32k", "latency", "50ms")
	if err != nil {
		logWarn("cannot set bandwidth rate limit on %s: %s", ap, strings.TrimSpace(out))
	} else {
		logInfo("bandwidth rate limit active: %d Mbps on %s", mbps, ap)
	}
}

func cleanupRateLimit(ap string) {
	if ap != "" {
		_, _ = runCmd("tc", "qdisc", "del", "dev", ap, "root")
	}
}

// --- VPN policy routing ------------------------------------------------------

// setupVPNRouting disables strict reverse-path filtering and adds a policy
// routing rule so AP traffic uses the WireGuard routing table.
// The original rp_filter values are saved to runDir for restore on cleanup.
func setupVPNRouting(ap, runDir string) {
	// Save originals before overwriting.
	origAP := readSysctl(rpFilterPath(ap))
	origAll := readSysctl(rpFilterAllSys)
	_ = os.WriteFile(filepath.Join(runDir, "rp_filter_ap.orig"), []byte(origAP), 0600)
	_ = os.WriteFile(filepath.Join(runDir, "rp_filter_all.orig"), []byte(origAll), 0600)

	_ = writeSysctl(rpFilterPath(ap), "0")
	_ = writeSysctl(rpFilterAllSys, "0")

	_, _ = runCmd("ip", "rule", "add", "iif", ap, "table", wireguardTable)
	logInfo("VPN policy routing rule active (iif %s -> table %s)", ap, wireguardTable)
}

// cleanupVPNRouting restores rp_filter to original values and removes the
// policy routing rule.
func cleanupVPNRouting(ap, runDir string) {
	if ap == "" {
		return
	}
	_, _ = runCmd("ip", "rule", "del", "iif", ap, "table", wireguardTable)

	// Restore rp_filter values saved during setup.
	if data, err := os.ReadFile(filepath.Join(runDir, "rp_filter_ap.orig")); err == nil {
		_ = writeSysctl(rpFilterPath(ap), strings.TrimSpace(string(data)))
	}
	if data, err := os.ReadFile(filepath.Join(runDir, "rp_filter_all.orig")); err == nil {
		_ = writeSysctl(rpFilterAllSys, strings.TrimSpace(string(data)))
	}
	_ = os.Remove(filepath.Join(runDir, "rp_filter_ap.orig"))
	_ = os.Remove(filepath.Join(runDir, "rp_filter_all.orig"))
}

// --- NAT sub-setup functions -------------------------------------------------

// setupTorRules installs transparent Tor proxying rules in the nat PREROUTING chain.
func setupTorRules(ap string) error {
	out, err := runIpt("-w", "-t", "nat", "-N", natPreChain)
	if err != nil && !isAlreadyExists(out) {
		return fmt.Errorf("cannot create nat prerouting chain: %w", err)
	}
	_, _ = runIpt("-w", "-t", "nat", "-A", natPreChain,
		"-i", ap, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-ports", torDNSPort)
	_, _ = runIpt("-w", "-t", "nat", "-A", natPreChain,
		"-i", ap, "-p", "tcp", "--dport", "53", "-j", "REDIRECT", "--to-ports", torDNSPort)
	_, _ = runIpt("-w", "-t", "nat", "-A", natPreChain,
		"-i", ap, "-p", "tcp", "--syn", "-j", "REDIRECT", "--to-ports", torTCPPort)
	if _, err := runIpt("-w", "-t", "nat", "-I", "PREROUTING", "-j", natPreChain); err != nil {
		return fmt.Errorf("cannot attach nat prerouting chain: %w", err)
	}
	logInfo("Tor transparent proxy active (redirecting TCP->%s, DNS->%s)", torTCPPort, torDNSPort)
	return nil
}

// setupVPNDNSRules forces all AP client DNS queries to go through the VPN
// tunnel using the configured VPN_DNS server.
func setupVPNDNSRules(ap, vpnDNS string) {
	out, err := runIpt("-w", "-t", "nat", "-N", natPreChain)
	if err != nil && !isAlreadyExists(out) {
		logWarn("cannot create nat prerouting chain for VPN DNS: %v", err)
		return
	}
	dest := vpnDNS + ":53"
	_, _ = runIpt("-w", "-t", "nat", "-A", natPreChain,
		"-i", ap, "-p", "udp", "--dport", "53", "-j", "DNAT", "--to-destination", dest)
	_, _ = runIpt("-w", "-t", "nat", "-A", natPreChain,
		"-i", ap, "-p", "tcp", "--dport", "53", "-j", "DNAT", "--to-destination", dest)
	if _, err := runIpt("-w", "-t", "nat", "-I", "PREROUTING", "-j", natPreChain); err == nil {
		logInfo("Forced VPN DNS tunnel active (AP clients DNS -> %s via VPN)", vpnDNS)
	}
}

// setupMasquerade installs MASQUERADE in the nat POSTROUTING chain for the uplink.
func setupMasquerade(uplink string) error {
	out, err := runIpt("-w", "-t", "nat", "-N", natPostChain)
	if err != nil && !isAlreadyExists(out) {
		return fmt.Errorf("cannot create nat postrouting chain: %w", err)
	}
	if _, err := runIpt("-w", "-t", "nat", "-A", natPostChain,
		"-o", uplink, "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("cannot add MASQUERADE rule: %w", err)
	}
	if _, err := runIpt("-w", "-t", "nat", "-I", "POSTROUTING", "-j", natPostChain); err != nil {
		return fmt.Errorf("cannot attach nat postrouting chain: %w", err)
	}
	return nil
}

// setupForwardRules allows forwarding between AP clients and the uplink,
// with an optional VPN kill-switch that drops non-VPN traffic.
func setupForwardRules(ap, uplink string, enableVPN, vpnKillSwitch bool) error {
	out, err := runIpt("-w", "-N", fwdChain)
	if err != nil && !isAlreadyExists(out) {
		return fmt.Errorf("cannot create forward chain: %w", err)
	}
	if _, err := runIpt("-w", "-A", fwdChain,
		"-i", ap, "-o", uplink, "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("cannot add forward ACCEPT rule: %w", err)
	}
	if _, err := runIpt("-w", "-A", fwdChain,
		"-i", uplink, "-o", ap, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("cannot add forward ESTABLISHED rule: %w", err)
	}
	if enableVPN && vpnKillSwitch {
		_, _ = runIpt("-w", "-A", fwdChain, "-i", ap, "-j", "DROP")
		logInfo("VPN Kill-Switch active (unencrypted fallback traffic blocked)")
	}
	if _, err := runIpt("-w", "-I", "FORWARD", "-j", fwdChain); err != nil {
		return fmt.Errorf("cannot attach forward chain: %w", err)
	}
	return nil
}

// setupHostIsolation blocks AP clients from reaching host services while
// still allowing DHCP and DNS.
func setupHostIsolation(ap string) error {
	out, err := runIpt("-w", "-N", inputChain)
	if err != nil && !isAlreadyExists(out) {
		return fmt.Errorf("cannot create input chain: %w", err)
	}
	// Allow DHCP (UDP 67/68)
	_, _ = runIpt("-w", "-A", inputChain,
		"-i", ap, "-p", "udp", "--dport", "67:68", "--sport", "67:68", "-j", "ACCEPT")
	// Allow DNS (UDP/TCP 53)
	_, _ = runIpt("-w", "-A", inputChain,
		"-i", ap, "-p", "udp", "--dport", "53", "-j", "ACCEPT")
	_, _ = runIpt("-w", "-A", inputChain,
		"-i", ap, "-p", "tcp", "--dport", "53", "-j", "ACCEPT")
	// Allow established/related
	_, _ = runIpt("-w", "-A", inputChain,
		"-i", ap, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	// Drop all other input from AP interface targeting the host
	_, _ = runIpt("-w", "-A", inputChain, "-i", ap, "-j", "DROP")
	if _, err := runIpt("-w", "-I", "INPUT", "-j", inputChain); err != nil {
		return fmt.Errorf("cannot attach input chain: %w", err)
	}
	logInfo("host isolation active (AP clients blocked from host services)")
	return nil
}

// setupMangleRules installs TTL spoofing and TCP MSS clamping in the mangle table.
// mssClamping is true when either VPN is active or DPI bypass mode is enabled.
func setupMangleRules(ap string, spoofTTL int, mssClamping bool) {
	out, err := runIpt("-w", "-t", "mangle", "-N", mangleChain)
	if err != nil && !isAlreadyExists(out) {
		logWarn("cannot create mangle chain: %v", err)
		return
	}
	if spoofTTL > 0 {
		_, _ = runIpt("-w", "-t", "mangle", "-A", mangleChain,
			"-i", ap, "-j", "TTL", "--ttl-set", strconv.Itoa(spoofTTL))
		logInfo("TTL spoofing active (TTL set to %d)", spoofTTL)
	}
	if mssClamping {
		_, _ = runIpt("-w", "-t", "mangle", "-A", mangleChain,
			"-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu")
		logInfo("TCP MSS Clamping active (anti-fragmentation)")
	}
	if _, err := runIpt("-w", "-t", "mangle", "-I", "PREROUTING", "-j", mangleChain); err != nil {
		logWarn("cannot attach mangle PREROUTING chain: %v", err)
	}
}

// NATConfig holds all parameters needed to configure NAT and firewall rules.
type NATConfig struct {
	RunDir        string
	Uplink        string
	AP            string
	IsolateHost   bool
	SpoofTTL      int
	TorMode       bool
	DisableIPv6   bool
	LimitRateMbps int
	EnableVPN     bool
	VPNKillSwitch bool
	VPNDNS        string
	DPIBypass     bool
}

// --- Public API --------------------------------------------------------------

// enableNAT saves the current forwarding state and installs all iptables rules.
func enableNAT(cfg NATConfig) error {

	// Persist current ip_forward value so cleanup can restore it.
	if err := os.WriteFile(filepath.Join(cfg.RunDir, "ip_forward.orig"),
		[]byte(readIPForward()), 0600); err != nil {
		return fmt.Errorf("cannot save ip_forward state: %w", err)
	}
	if err := setIPForward(1); err != nil {
		return fmt.Errorf("cannot enable ip_forward: %w", err)
	}

	// 1. IPv6 leak protection.
	if cfg.DisableIPv6 {
		setupIPv6LeakProtection(cfg.AP)
	}

	// 2. VPN policy routing (saves rp_filter originals to runDir).
	// Not applied for dpibypass — there is no VPN tunnel interface.
	if cfg.EnableVPN {
		setupVPNRouting(cfg.AP, cfg.RunDir)
	}

	// 3. DNS interception: Tor mode OR forced VPN DNS.
	// Not applied for dpibypass — traffic goes out on normal uplink.
	if cfg.TorMode {
		if err := setupTorRules(cfg.AP); err != nil {
			return err
		}
	} else if cfg.EnableVPN {
		setupVPNDNSRules(cfg.AP, cfg.VPNDNS)
	}

	// 4. NAT POSTROUTING masquerade.
	if err := setupMasquerade(cfg.Uplink); err != nil {
		return err
	}

	// 5. FORWARD chain with optional kill-switch.
	// Kill-switch is only meaningful when there is an actual VPN tunnel.
	if err := setupForwardRules(cfg.AP, cfg.Uplink, cfg.EnableVPN, cfg.VPNKillSwitch); err != nil {
		return err
	}

	// 6. Host isolation (INPUT chain).
	if cfg.IsolateHost {
		if err := setupHostIsolation(cfg.AP); err != nil {
			return err
		}
	}

	// 7. Mangle: TTL spoofing & TCP MSS clamping.
	// MSS clamping is active for VPN mode (to handle MTU reduction) AND
	// for dpibypass mode (to help bypass stateful DPI).
	mssClamping := cfg.EnableVPN || cfg.DPIBypass
	if cfg.SpoofTTL > 0 || mssClamping {
		setupMangleRules(cfg.AP, cfg.SpoofTTL, mssClamping)
	}

	// 8. Bandwidth rate limiting.
	setupRateLimit(cfg.AP, cfg.LimitRateMbps)

	return nil
}

// disableNAT removes all routerd iptables rules and restores saved sysctl values.
func disableNAT(runDir, ap string) {
	// Tear down all routerd chains.
	cleanupIPv6LeakProtection(ap)
	cleanupVPNRouting(ap, runDir)
	cleanupRateLimit(ap)

	// nat PREROUTING
	_, _ = runIpt("-w", "-t", "nat", "-D", "PREROUTING", "-j", natPreChain)
	_, _ = runIpt("-w", "-t", "nat", "-F", natPreChain)
	_, _ = runIpt("-w", "-t", "nat", "-X", natPreChain)

	// nat POSTROUTING
	_, _ = runIpt("-w", "-t", "nat", "-D", "POSTROUTING", "-j", natPostChain)
	_, _ = runIpt("-w", "-t", "nat", "-F", natPostChain)
	_, _ = runIpt("-w", "-t", "nat", "-X", natPostChain)

	// filter FORWARD
	_, _ = runIpt("-w", "-D", "FORWARD", "-j", fwdChain)
	_, _ = runIpt("-w", "-F", fwdChain)
	_, _ = runIpt("-w", "-X", fwdChain)

	// filter INPUT
	_, _ = runIpt("-w", "-D", "INPUT", "-j", inputChain)
	_, _ = runIpt("-w", "-F", inputChain)
	_, _ = runIpt("-w", "-X", inputChain)

	// mangle PREROUTING
	_, _ = runIpt("-w", "-t", "mangle", "-D", "PREROUTING", "-j", mangleChain)
	_, _ = runIpt("-w", "-t", "mangle", "-F", mangleChain)
	_, _ = runIpt("-w", "-t", "mangle", "-X", mangleChain)

	// Restore ip_forward.
	if data, err := os.ReadFile(filepath.Join(runDir, "ip_forward.orig")); err == nil {
		_ = setIPForward(atoiSafe(strings.TrimSpace(string(data))))
	}
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
