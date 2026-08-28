package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	iptablesCmd  = "iptables"
	ip6tablesCmd = "ip6tables"
)

const (
	natPostChain = "ROUTERD_POST"
	natPreChain  = "ROUTERD_PRE"
	fwdChain     = "ROUTERD_FWD"
	inputChain   = "ROUTERD_IN"
	mangleChain  = "ROUTERD_MANGLE"
	forwardSys   = "/proc/sys/net/ipv4/ip_forward"
)

func readIPForward() string {
	data, err := os.ReadFile(forwardSys)
	if err != nil {
		return "0"
	}
	return strings.TrimSpace(string(data))
}

func setIPForward(v int) error {
	return os.WriteFile(forwardSys, []byte(strconv.Itoa(v)), 0644)
}

func setupIPv6LeakProtection(ap string, disableIPv6 bool) {
	if !disableIPv6 {
		return
	}
	sysctlPath := fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", ap)
	if err := os.WriteFile(sysctlPath, []byte("1"), 0644); err != nil {
		logWarn("cannot disable IPv6 on %s via sysctl: %v", ap, err)
	} else {
		logInfo("IPv6 disabled on %s interface (sysctl)", ap)
	}

	_, _ = runCmd(ip6tablesCmd, "-w", "-I", "INPUT", "-i", ap, "-j", "DROP")
	_, _ = runCmd(ip6tablesCmd, "-w", "-I", "FORWARD", "-i", ap, "-j", "DROP")
	_, _ = runCmd(ip6tablesCmd, "-w", "-I", "FORWARD", "-o", ap, "-j", "DROP")
	logInfo("IPv6 leak protection rules active (ip6tables DROP on %s)", ap)
}

func cleanupIPv6LeakProtection(ap string) {
	if ap != "" {
		sysctlPath := fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", ap)
		_ = os.WriteFile(sysctlPath, []byte("0"), 0644)
		_, _ = runCmd(ip6tablesCmd, "-w", "-D", "INPUT", "-i", ap, "-j", "DROP")
		_, _ = runCmd(ip6tablesCmd, "-w", "-D", "FORWARD", "-i", ap, "-j", "DROP")
		_, _ = runCmd(ip6tablesCmd, "-w", "-D", "FORWARD", "-o", ap, "-j", "DROP")
	}
}

func setupRateLimit(ap string, mbps int) {
	if mbps <= 0 {
		return
	}
	out, err := runCmd("tc", "qdisc", "add", "dev", ap, "root", "tbf", "rate", fmt.Sprintf("%dmbit", mbps), "burst", "32k", "latency", "50ms")
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

func setupVPNRouting(ap string, enableVPN bool) {
	if !enableVPN {
		return
	}
	// Disable strict reverse path filtering on AP and VPN interfaces to prevent Linux kernel from dropping asymmetrical routed packets
	_ = os.WriteFile(fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", ap), []byte("0"), 0644)
	_ = os.WriteFile("/proc/sys/net/ipv4/conf/all/rp_filter", []byte("0"), 0644)

	// Add policy routing rule so packets from AP interface use WireGuard table (51820)
	_, _ = runCmd("ip", "rule", "add", "iif", ap, "table", "51820")
	logInfo("VPN policy routing rule active (iif %s -> table 51820)", ap)
}

func cleanupVPNRouting(ap string) {
	if ap != "" {
		_, _ = runCmd("ip", "rule", "del", "iif", ap, "table", "51820")
	}
}

// enableNAT saves the current forwarding state and installs iptables rules.
func enableNAT(runDir, uplink, ap string, isolateHost bool, spoofTTL int, torMode bool, disableIPv6 bool, limitRateMbps int, enableVPN bool, vpnKillSwitch bool) error {
	if err := os.WriteFile(filepath.Join(runDir, "ip_forward.orig"),
		[]byte(readIPForward()), 0600); err != nil {
		return err
	}
	if err := setIPForward(1); err != nil {
		return err
	}

	// 1. IPv6 Leak Protection
	setupIPv6LeakProtection(ap, disableIPv6)

	// 1b. VPN Policy Routing
	setupVPNRouting(ap, enableVPN)

	// 2. Tor Mode or Forced VPN DNS (PREROUTING nat)
	if torMode {
		if out, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-N", natPreChain); err != nil &&
			!strings.Contains(out, "already exists") {
			return fmt.Errorf("cannot create nat prerouting chain: %w", err)
		}
		_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-A", natPreChain, "-i", ap, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-ports", "5353")
		_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-A", natPreChain, "-i", ap, "-p", "tcp", "--dport", "53", "-j", "REDIRECT", "--to-ports", "5353")
		_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-A", natPreChain, "-i", ap, "-p", "tcp", "--syn", "-j", "REDIRECT", "--to-ports", "9040")
		if _, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-I", "PREROUTING", "-j", natPreChain); err != nil {
			return fmt.Errorf("cannot attach nat prerouting chain: %w", err)
		}
		logInfo("Tor transparent proxy active (redirecting TCP->9040, DNS->5353)")
	} else if enableVPN {
		if out, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-N", natPreChain); err == nil ||
			strings.Contains(out, "already exists") {
			_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-A", natPreChain, "-i", ap, "-p", "udp", "--dport", "53", "-j", "DNAT", "--to-destination", "1.1.1.1:53")
			_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-A", natPreChain, "-i", ap, "-p", "tcp", "--dport", "53", "-j", "DNAT", "--to-destination", "1.1.1.1:53")
			if _, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-I", "PREROUTING", "-j", natPreChain); err == nil {
				logInfo("Forced VPN DNS tunnel active (AP clients DNS -> 1.1.1.1 via WireGuard)")
			}
		}
	}

	// 3. NAT POSTROUTING: masquerade traffic leaving on the uplink.
	if out, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-N", natPostChain); err != nil &&
		!strings.Contains(out, "already exists") {
		return err
	}
	if _, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-A", natPostChain, "-o", uplink, "-j", "MASQUERADE"); err != nil {
		return err
	}
	if _, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-I", "POSTROUTING", "-j", natPostChain); err != nil {
		return err
	}

	// 4. FORWARD: allow forwarding between AP clients and the uplink.
	if out, err := runCmd(iptablesCmd, "-w", "-N", fwdChain); err != nil &&
		!strings.Contains(out, "already exists") {
		return err
	}
	if _, err := runCmd(iptablesCmd, "-w", "-A", fwdChain, "-i", ap, "-o", uplink, "-j", "ACCEPT"); err != nil {
		return err
	}
	if _, err := runCmd(iptablesCmd, "-w", "-A", fwdChain, "-i", uplink, "-o", ap,
		"-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"); err != nil {
		return err
	}

	// If VPN Kill-Switch is active, drop any client forwarding that is NOT using the designated VPN uplink
	if enableVPN && vpnKillSwitch {
		_, _ = runCmd(iptablesCmd, "-w", "-A", fwdChain, "-i", ap, "-j", "DROP")
		logInfo("VPN Kill-Switch active (unencrypted fallback traffic blocked)")
	}

	if _, err := runCmd(iptablesCmd, "-w", "-I", "FORWARD", "-j", fwdChain); err != nil {
		return err
	}

	// 5. Host Isolation: block AP clients from accessing local host ports/services.
	if isolateHost {
		if out, err := runCmd(iptablesCmd, "-w", "-N", inputChain); err != nil &&
			!strings.Contains(out, "already exists") {
			return err
		}
		// Allow DHCP (UDP 67/68)
		_, _ = runCmd(iptablesCmd, "-w", "-A", inputChain, "-i", ap, "-p", "udp", "--dport", "67:68", "--sport", "67:68", "-j", "ACCEPT")
		// Allow DNS (UDP/TCP 53)
		_, _ = runCmd(iptablesCmd, "-w", "-A", inputChain, "-i", ap, "-p", "udp", "--dport", "53", "-j", "ACCEPT")
		_, _ = runCmd(iptablesCmd, "-w", "-A", inputChain, "-i", ap, "-p", "tcp", "--dport", "53", "-j", "ACCEPT")
		// Allow established/related
		_, _ = runCmd(iptablesCmd, "-w", "-A", inputChain, "-i", ap, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
		// Drop all other input from AP interface targeting the host
		_, _ = runCmd(iptablesCmd, "-w", "-A", inputChain, "-i", ap, "-j", "DROP")
		if _, err := runCmd(iptablesCmd, "-w", "-I", "INPUT", "-j", inputChain); err != nil {
			return err
		}
		logInfo("host isolation active (AP clients blocked from host services)")
	}

	// 6. MANGLE TABLE: TTL Spoofing & TCP MSS Clamping
	if spoofTTL > 0 || enableVPN {
		if out, err := runCmd(iptablesCmd, "-w", "-t", "mangle", "-N", mangleChain); err == nil ||
			strings.Contains(out, "already exists") {
			if spoofTTL > 0 {
				_, _ = runCmd(iptablesCmd, "-w", "-t", "mangle", "-A", mangleChain, "-i", ap, "-j", "TTL", "--ttl-set", strconv.Itoa(spoofTTL))
				logInfo("TTL spoofing active (TTL set to %d)", spoofTTL)
			}
			if enableVPN {
				_, _ = runCmd(iptablesCmd, "-w", "-t", "mangle", "-A", mangleChain, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu")
				logInfo("TCP MSS Clamping active (anti-fragmentation for VPN tunnel)")
			}
			if _, err := runCmd(iptablesCmd, "-w", "-t", "mangle", "-I", "PREROUTING", "-j", mangleChain); err != nil {
				logWarn("cannot attach mangle PREROUTING chain: %v", err)
			}
		} else {
			logWarn("cannot create mangle chain: %v", err)
		}
	}

	// 7. Rate Limiting
	setupRateLimit(ap, limitRateMbps)

	return nil
}

func disableNAT(runDir, ap string) {
	cleanupIPv6LeakProtection(ap)
	cleanupVPNRouting(ap)
	cleanupRateLimit(ap)

	// PREROUTING nat
	_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-D", "PREROUTING", "-j", natPreChain)
	_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-F", natPreChain)
	_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-X", natPreChain)

	// POSTROUTING nat
	_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-D", "POSTROUTING", "-j", natPostChain)
	_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-F", natPostChain)
	_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-X", natPostChain)

	// FORWARD filter
	_, _ = runCmd(iptablesCmd, "-w", "-D", "FORWARD", "-j", fwdChain)
	_, _ = runCmd(iptablesCmd, "-w", "-F", fwdChain)
	_, _ = runCmd(iptablesCmd, "-w", "-X", fwdChain)

	// INPUT filter
	_, _ = runCmd(iptablesCmd, "-w", "-D", "INPUT", "-j", inputChain)
	_, _ = runCmd(iptablesCmd, "-w", "-F", inputChain)
	_, _ = runCmd(iptablesCmd, "-w", "-X", inputChain)

	// PREROUTING mangle
	_, _ = runCmd(iptablesCmd, "-w", "-t", "mangle", "-D", "PREROUTING", "-j", mangleChain)
	_, _ = runCmd(iptablesCmd, "-w", "-t", "mangle", "-F", mangleChain)
	_, _ = runCmd(iptablesCmd, "-w", "-t", "mangle", "-X", mangleChain)

	if data, err := os.ReadFile(filepath.Join(runDir, "ip_forward.orig")); err == nil {
		_ = setIPForward(atoiSafe(strings.TrimSpace(string(data))))
	}
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
