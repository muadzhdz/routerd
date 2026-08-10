package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const iptablesCmd = "iptables"

const (
	natChain   = "ROUTERD_POST"
	fwdChain   = "ROUTERD_FWD"
	forwardSys = "/proc/sys/net/ipv4/ip_forward"
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

// enableNAT saves the current forwarding state and installs iptables rules.
func enableNAT(runDir, uplink, ap string) error {
	if err := os.WriteFile(filepath.Join(runDir, "ip_forward.orig"),
		[]byte(readIPForward()), 0600); err != nil {
		return err
	}
	if err := setIPForward(1); err != nil {
		return err
	}

	// NAT: masquerade traffic leaving on the uplink.
	if out, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-N", natChain); err != nil &&
		!strings.Contains(out, "already exists") {
		return err
	}
	if _, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-A", natChain, "-o", uplink, "-j", "MASQUERADE"); err != nil {
		return err
	}
	if _, err := runCmd(iptablesCmd, "-w", "-t", "nat", "-I", "POSTROUTING", "-j", natChain); err != nil {
		return err
	}

	// Filter: allow forwarding between AP clients and the uplink.
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
	if _, err := runCmd(iptablesCmd, "-w", "-I", "FORWARD", "-j", fwdChain); err != nil {
		return err
	}
	return nil
}

func disableNAT(runDir string) {
	_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-D", "POSTROUTING", "-j", natChain)
	_, _ = runCmd(iptablesCmd, "-w", "-D", "FORWARD", "-j", fwdChain)
	_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-F", natChain)
	_, _ = runCmd(iptablesCmd, "-w", "-F", fwdChain)
	_, _ = runCmd(iptablesCmd, "-w", "-t", "nat", "-X", natChain)
	_, _ = runCmd(iptablesCmd, "-w", "-X", fwdChain)

	if data, err := os.ReadFile(filepath.Join(runDir, "ip_forward.orig")); err == nil {
		_ = setIPForward(atoiSafe(strings.TrimSpace(string(data))))
	}
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
