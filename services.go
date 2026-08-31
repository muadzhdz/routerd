package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Proc is a long-running child process managed by routerd.
type Proc struct {
	name string
	cmd  *exec.Cmd
	log  *os.File
}

// ProcessManager manages child processes with a mutex to prevent races.
type ProcessManager struct {
	mu    sync.Mutex
	procs []*Proc
}

var procMgr = &ProcessManager{}

// spawn starts a named process, redirecting stdout+stderr to a log file.
// It appends the process to the manager's process list under the mutex.
func (pm *ProcessManager) spawn(name, runDir string, args ...string) error {
	logFile, err := os.OpenFile(filepath.Join(runDir, name+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("cannot open log file for %s: %v", name, err)
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("cannot start %s: %v", name, err)
	}
	pm.mu.Lock()
	pm.procs = append(pm.procs, &Proc{name: name, cmd: cmd, log: logFile})
	pm.mu.Unlock()
	return nil
}

// stopAll sends SIGINT to every managed process and waits up to 5 seconds
// before force-killing. Safe to call concurrently.
func (pm *ProcessManager) stopAll() {
	pm.mu.Lock()
	list := pm.procs
	pm.procs = nil
	pm.mu.Unlock()

	for _, p := range list {
		if p == nil || p.cmd == nil || p.cmd.Process == nil {
			continue
		}
		done := make(chan struct{})
		go func(p *Proc) {
			_ = p.cmd.Wait()
			close(done)
		}(p)
		_ = p.cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
		}
		if p.log != nil {
			p.log.Close()
		}
	}
}

// watchdog starts a goroutine that monitors the named process (hostapd or
// dnsmasq). If the process exits unexpectedly the provided onExit callback is
// called with the process name, allowing the caller to trigger a full cleanup.
// Detection is done via pgrep rather than ProcessState, because ProcessState
// is only populated after cmd.Wait() returns — which we cannot call from here
// without racing against stopAll().
func (pm *ProcessManager) watchdog(name string, onExit func(string)) {
	go func() {
		for {
			time.Sleep(2 * time.Second)

			// Check if the process is still in our managed list.
			pm.mu.Lock()
			var found bool
			for _, p := range pm.procs {
				if p.name == name {
					found = true
					break
				}
			}
			pm.mu.Unlock()

			if !found {
				// Process was removed by stopAll — exit watchdog silently.
				return
			}

			// Use pgrep to check if the process is actually alive.
			// This avoids the ProcessState race with cmd.Wait().
			switch name {
			case "hostapd":
				if !hostapdRunning() {
					logWarn("watchdog: %s exited unexpectedly", name)
					onExit(name)
					return
				}
			case "dnsmasq":
				if !dnsmasqRunning() {
					logWarn("watchdog: %s exited unexpectedly", name)
					onExit(name)
					return
				}
			}
		}
	}()
}

// Package-level wrappers for backward compatibility.
func spawn(name, runDir string, args ...string) error {
	return procMgr.spawn(name, runDir, args...)
}

func stopProcs() {
	procMgr.stopAll()
}

// --- IP helpers -------------------------------------------------------------

// ipNetmask returns the dotted-decimal subnet mask of a CIDR string.
// Returns "255.255.255.0" as a safe fallback if cidr is invalid.
func ipNetmask(cidr string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || ipnet == nil {
		return "255.255.255.0"
	}
	return net.IP(ipnet.Mask).String()
}

func incIP(ip net.IP) net.IP {
	out := make(net.IP, 4)
	copy(out, ip.To4())
	for i := 3; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			break
		}
	}
	return out
}

func decIP(ip net.IP) net.IP {
	out := make(net.IP, 4)
	copy(out, ip.To4())
	for i := 3; i >= 0; i-- {
		out[i]--
		if out[i] != 0xff {
			break
		}
	}
	return out
}

// dhcpRange returns the start/end IPs for the DHCP pool of a subnet:
// start = base + 10, end = broadcast - 1.
func dhcpRange(cidr string) (net.IP, net.IP, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, err
	}
	base := ipnet.IP.To4()
	bc := make(net.IP, 4)
	copy(bc, base)
	for i, b := range ipnet.Mask {
		bc[i] |= ^b
	}
	start := incIP(base)
	for i := 0; i < 9; i++ {
		start = incIP(start)
	}
	return start, decIP(bc), nil
}

// --- Config generators ------------------------------------------------------

// hostapdConf generates the hostapd configuration for the given channel and band.
func hostapdConf(cfg *Config, ch int, band string) string {
	hw := "g"
	if band == "a" {
		hw = "a"
	}
	var sb []byte
	add := func(k, v string) { sb = append(sb, fmt.Sprintf("%s=%s\n", k, v)...) }

	add("interface", cfg.InterfaceAP)
	add("driver", "nl80211")
	add("ssid", cfg.SSID)
	if cfg.HideSSID {
		add("ignore_broadcast_ssid", "1")
	}
	add("hw_mode", hw)
	add("channel", fmt.Sprintf("%d", ch))
	if cfg.Country != "" {
		add("country_code", cfg.Country)
	}
	add("ieee80211n", "1")
	if band == "a" {
		add("ieee80211ac", "1")
	}
	add("ieee80211d", "1")
	add("wmm_enabled", "1")
	add("max_num_sta", fmt.Sprintf("%d", cfg.MaxClients))

	if cfg.Password != "" {
		if cfg.WPA3 {
			// WPA3-SAE with WPA2-PSK transition mode for compatibility.
			add("auth_algs", "1")
			add("wpa", "2")
			add("wpa_key_mgmt", "SAE WPA-PSK")
			add("wpa_pairwise", "CCMP")
			add("rsn_pairwise", "CCMP")
			add("sae_password", cfg.Password)
			add("wpa_passphrase", cfg.Password)
			add("ieee80211w", "1") // Management Frame Protection optional (transition)
			logInfo("WPA3-SAE/WPA2-PSK transition mode enabled")
		} else {
			add("auth_algs", "1")
			add("wpa", "2")
			add("wpa_key_mgmt", "WPA-PSK")
			add("wpa_pairwise", "CCMP")
			add("rsn_pairwise", "CCMP")
			add("wpa_passphrase", cfg.Password)
		}
	}
	return string(sb)
}

// dnsmasqConf generates the dnsmasq configuration.
func dnsmasqConf(cfg *Config) (string, error) {
	gw, err := gatewayIP(cfg.Subnet)
	if err != nil {
		return "", err
	}
	start, end, err := dhcpRange(cfg.Subnet)
	if err != nil {
		return "", err
	}
	var sb []byte
	add := func(k, v string) {
		if v == "" {
			sb = append(sb, fmt.Sprintf("%s\n", k)...)
		} else {
			sb = append(sb, fmt.Sprintf("%s=%s\n", k, v)...)
		}
	}

	add("port", "53")
	add("no-resolv", "")

	// When VPN is active and the default DNS is a local stub resolver, use the
	// configured VPN DNS server so queries go through the encrypted tunnel.
	dnsServer := cfg.DNS
	if cfg.EnableVPN && (dnsServer == "127.0.0.53" || dnsServer == "127.0.0.1" || dnsServer == "") {
		dnsServer = cfg.VPNDNS
	}
	add("server", dnsServer)
	if cfg.EnableVPN && dnsServer == cfg.VPNDNS && cfg.VPNDNS == "1.1.1.1" {
		// Add Cloudflare secondary only when using default WARP/Cloudflare DNS.
		add("server", "1.0.0.1")
	}

	add("interface", cfg.InterfaceAP)
	add("bind-interfaces", "")
	add("listen-address", gw.String())
	add("dhcp-range", fmt.Sprintf("%s,%s,%s,12h", start.String(), end.String(), ipNetmask(cfg.Subnet)))
	add("dhcp-option", "option:router,"+gw.String())
	add("dhcp-option", "option:dns-server,"+gw.String())
	add("dhcp-authoritative", "")
	return string(sb), nil
}

// --- Service start helpers --------------------------------------------------

// startHostapd writes the hostapd config and waits (by polling the log) until
// the AP is enabled or a 10-second deadline elapses.
func startHostapd(cfg *Config, ch int, band, runDir string) error {
	conf := hostapdConf(cfg, ch, band)
	if err := os.WriteFile(filepath.Join(runDir, "hostapd.conf"), []byte(conf), 0600); err != nil {
		return err
	}
	if err := spawn("hostapd", runDir, filepath.Join(runDir, "hostapd.conf")); err != nil {
		return err
	}
	// Poll until the AP is enabled or hostapd dies.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		data, _ := os.ReadFile(filepath.Join(runDir, "hostapd.log"))
		if bytes.Contains(data, []byte("AP-ENABLED")) {
			return nil
		}
		if !hostapdRunning() {
			return fmt.Errorf("hostapd exited during startup (see %s/hostapd.log)", runDir)
		}
	}
	return fmt.Errorf("timed out waiting for hostapd to enable the AP (see %s/hostapd.log)", runDir)
}

// startDnsmasq writes the dnsmasq config and waits (by polling the log) until
// the daemon is ready or a 5-second deadline elapses.
func startDnsmasq(cfg *Config, runDir string) error {
	dconf, err := dnsmasqConf(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runDir, "dnsmasq.conf"), []byte(dconf), 0600); err != nil {
		return err
	}
	if err := spawn("dnsmasq", runDir,
		"--keep-in-foreground",
		"--conf-file="+filepath.Join(runDir, "dnsmasq.conf")); err != nil {
		return err
	}
	// Poll the log for a ready signal instead of sleeping blindly.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		data, _ := os.ReadFile(filepath.Join(runDir, "dnsmasq.log"))
		if bytes.Contains(data, []byte("started")) || bytes.Contains(data, []byte("DHCP")) {
			return nil
		}
		if !dnsmasqRunning() {
			return fmt.Errorf("dnsmasq exited during startup (see %s/dnsmasq.log)", runDir)
		}
	}
	// If deadline elapsed but process is still running, treat as OK — some
	// builds produce no startup message.
	if dnsmasqRunning() {
		return nil
	}
	return fmt.Errorf("timed out waiting for dnsmasq to start (see %s/dnsmasq.log)", runDir)
}
