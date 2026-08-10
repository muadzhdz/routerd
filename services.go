package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Proc is a long-running child process managed by routerd.
type Proc struct {
	name string
	cmd  *exec.Cmd
	log  *os.File
}

var procs []*Proc

func spawn(name, runDir string, args ...string) error {
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
	procs = append(procs, &Proc{name: name, cmd: cmd, log: logFile})
	return nil
}

func stopProcs() {
	for _, p := range procs {
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
	procs = nil
}

func ipNetmask(cidr string) string {
	_, ipnet, _ := net.ParseCIDR(cidr)
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
		add("auth_algs", "1")
		add("wpa", "2")
		add("wpa_key_mgmt", "WPA-PSK")
		add("wpa_pairwise", "CCMP")
		add("rsn_pairwise", "CCMP")
		add("wpa_passphrase", cfg.Password)
	}
	return string(sb)
}

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
	add("server", cfg.DNS)
	add("interface", cfg.InterfaceAP)
	add("bind-interfaces", "")
	add("listen-address", gw.String())
	add("dhcp-range", fmt.Sprintf("%s,%s,%s,12h", start.String(), end.String(), ipNetmask(cfg.Subnet)))
	add("dhcp-option", "option:router,"+gw.String())
	add("dhcp-option", "option:dns-server,"+gw.String())
	add("dhcp-authoritative", "")
	return string(sb), nil
}

// startHostapd writes and starts hostapd. It returns once the AP is enabled
// (so the caller can assign the interface address afterwards).
func startHostapd(cfg *Config, ch int, band, runDir string) error {
	conf := hostapdConf(cfg, ch, band)
	if err := os.WriteFile(filepath.Join(runDir, "hostapd.conf"), []byte(conf), 0600); err != nil {
		return err
	}
	if err := spawn("hostapd", runDir, filepath.Join(runDir, "hostapd.conf")); err != nil {
		return err
	}
	// poll until the AP is enabled (or hostapd dies)
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

func startDnsmasq(cfg *Config, runDir string) error {
	dconf, err := dnsmasqConf(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runDir, "dnsmasq.conf"), []byte(dconf), 0600); err != nil {
		return err
	}
	if err := spawn("dnsmasq", runDir, "--keep-in-foreground", "--conf-file="+filepath.Join(runDir, "dnsmasq.conf")); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	if !dnsmasqRunning() {
		return fmt.Errorf("dnsmasq exited during startup (see %s/dnsmasq.log)", runDir)
	}
	return nil
}
