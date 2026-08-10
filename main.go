package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const version = "0.1.0"
const runDir = "/run/routerd"

var configPath = defaultConfigPath

func logInfo(format string, a ...any) { log.Printf("[info] "+format, a...) }
func logWarn(format string, a ...any) { log.Printf("[warn] "+format, a...) }

func usage() {
	fmt.Println(`routerd - turn a Linux machine into a WiFi access point / router.

Usage:
  routerd [options] <command>

Commands:
  start     Bring up the access point and NAT routing (foreground).
  stop      Tear everything down (interfaces, DHCP, NAT rules).
  status    Show the current state of the access point.
  reload    Regenerate hostapd/dnsmasq configs and restart them.
  version   Print the version.

Options:
  -c, --config PATH   Config file (default /etc/routerd.conf).
  -h, --help          Show this help.

Examples:
  sudo systemctl start routerd
  sudo routerd status
  sudo routerd -c /etc/routerd.conf start`)
}

type State struct {
	SSID         string
	InterfaceAP  string
	InterfaceSTA string
	Uplink       string
	Channel      int
	Band         string
	Subnet       string
}

func writeState(s State) {
	_ = os.MkdirAll(runDir, 0755)
	var b strings.Builder
	fmt.Fprintf(&b, "SSID=%s\n", s.SSID)
	fmt.Fprintf(&b, "INTERFACE_AP=%s\n", s.InterfaceAP)
	fmt.Fprintf(&b, "INTERFACE_STA=%s\n", s.InterfaceSTA)
	fmt.Fprintf(&b, "UPLINK=%s\n", s.Uplink)
	fmt.Fprintf(&b, "CHANNEL=%d\n", s.Channel)
	fmt.Fprintf(&b, "BAND=%s\n", s.Band)
	fmt.Fprintf(&b, "SUBNET=%s\n", s.Subnet)
	if err := os.WriteFile(filepath.Join(runDir, "state"), []byte(b.String()), 0644); err != nil {
		logWarn("cannot write state file: %v", err)
	}
}

func readState() (State, bool) {
	var s State
	data, err := os.ReadFile(filepath.Join(runDir, "state"))
	if err != nil {
		return s, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "SSID":
			s.SSID = val
		case "INTERFACE_AP":
			s.InterfaceAP = val
		case "INTERFACE_STA":
			s.InterfaceSTA = val
		case "UPLINK":
			s.Uplink = val
		case "CHANNEL":
			s.Channel, _ = strconv.Atoi(val)
		case "BAND":
			s.Band = val
		case "SUBNET":
			s.Subnet = val
		}
	}
	return s, s.InterfaceAP != ""
}

func hostapdRunning() bool {
	out, err := runCmd("pgrep", "-f", "hostapd .*/routerd/hostapd.conf")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

func dnsmasqRunning() bool {
	out, err := runCmd("pgrep", "-f", "dnsmasq .*/routerd/dnsmasq.conf")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// killOrphans terminates hostapd/dnsmasq that might be left over from a
// previous run that was killed hard.
func killOrphans() {
	_, _ = runCmd("pkill", "-f", "hostapd .*/routerd/hostapd.conf")
	_, _ = runCmd("pkill", "-f", "dnsmasq .*/routerd/dnsmasq.conf")
}

func cleanup(cfg *Config) {
	stopProcs()
	killOrphans()
	if cfg != nil {
		disableNAT(runDir)
		if gwCIDR, err := gatewayCIDR(cfg.Subnet); err == nil {
			addrDel(cfg.InterfaceAP, gwCIDR)
		}
		deleteAPInterface(cfg.InterfaceAP)
	}
	_ = os.Remove(filepath.Join(runDir, "state"))
	_ = os.Remove(filepath.Join(runDir, "ip_forward.orig"))
	// remove runtime files (they contain the Wi-Fi password)
	for _, f := range []string{"hostapd.conf", "dnsmasq.conf", "hostapd.pid", "dnsmasq.pid"} {
		_ = os.Remove(filepath.Join(runDir, f))
	}
}

func cmdStart() {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := os.MkdirAll(runDir, 0755); err != nil {
		log.Fatalf("cannot create %s: %v", runDir, err)
	}

	cleanup(cfg) // remove any stale state from a previous run

	failed := true
	defer func() {
		if failed {
			cleanup(cfg)
		}
	}()

	sta, err := findSTAInterface(cfg)
	if err != nil {
		log.Fatalf("%v", err)
	}
	uplink, err := detectUplink(cfg, sta)
	if err != nil {
		log.Fatalf("%v", err)
	}

	var ch int
	var band string
	if cfg.Channel == "" || cfg.Channel == "auto" {
		ch, band, err = detectChannel(sta)
		if err != nil {
			log.Fatalf("%v", err)
		}
		logInfo("auto-detected channel %d (%s band) from %s", ch, band, sta)
	} else {
		ch, err = strconv.Atoi(cfg.Channel)
		if err != nil || ch < 1 || ch > 165 {
			log.Fatalf("invalid CHANNEL %q (use auto or 1-165)", cfg.Channel)
		}
		if ch <= 14 {
			band = "g"
		} else {
			band = "a"
		}
	}

	cfg.InterfaceSTA = sta
	cfg.Uplink = uplink

	gwCIDR, err := gatewayCIDR(cfg.Subnet)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if err := createAPInterface(sta, cfg.InterfaceAP); err != nil {
		log.Fatalf("%v", err)
	}
	if err := interfaceUp(cfg.InterfaceAP); err != nil {
		log.Fatalf("%v", err)
	}
	setUnmanaged(cfg.InterfaceAP)

	// hostapd resets the interface when it enters AP mode, so the IPv4
	// address must be assigned only after the AP is up.
	if err := startHostapd(cfg, ch, band, runDir); err != nil {
		log.Fatalf("%v", err)
	}
	if err := addrAdd(cfg.InterfaceAP, gwCIDR); err != nil {
		log.Fatalf("%v", err)
	}
	if err := startDnsmasq(cfg, runDir); err != nil {
		log.Fatalf("%v", err)
	}
	if err := enableNAT(runDir, uplink, cfg.InterfaceAP); err != nil {
		log.Fatalf("%v", err)
	}

	writeState(State{
		SSID: cfg.SSID, InterfaceAP: cfg.InterfaceAP, InterfaceSTA: sta,
		Uplink: uplink, Channel: ch, Band: band, Subnet: cfg.Subnet,
	})
	failed = false

	logInfo("routerd running: ssid=%q channel=%d band=%s ap=%s sta=%s uplink=%s subnet=%s clients=%d",
		cfg.SSID, ch, band, cfg.InterfaceAP, sta, uplink, cfg.Subnet, cfg.MaxClients)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	logInfo("shutting down")
	cleanup(cfg)
	logInfo("stopped")
}

func cmdStop() {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		cfg = DefaultConfig()
	}
	cleanup(cfg)
	logInfo("stopped")
}

func cmdStatus() {
	st, ok := readState()
	if !ok {
		fmt.Println("routerd: not running")
		return
	}
	if !hostapdRunning() {
		fmt.Println("routerd: NOT running (hostapd is down)")
		return
	}
	clients := countStations(st.InterfaceAP)
	fmt.Println("routerd is running:")
	fmt.Printf("  SSID:      %s\n", st.SSID)
	fmt.Printf("  Channel:   %d (%s band)\n", st.Channel, st.Band)
	fmt.Printf("  AP iface:  %s (%s)\n", st.InterfaceAP, st.Subnet)
	fmt.Printf("  STA iface: %s\n", st.InterfaceSTA)
	fmt.Printf("  Uplink:    %s\n", st.Uplink)
	if clients < 0 {
		fmt.Println("  Clients:   n/a")
	} else {
		fmt.Printf("  Clients:   %d\n", clients)
	}
}

func cmdReload() {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	st, ok := readState()
	if !ok {
		log.Fatalf("routerd is not running (no state found)")
	}
	stopProcs()
	killOrphans()
	if err := startHostapd(cfg, st.Channel, st.Band, runDir); err != nil {
		cleanup(cfg)
		log.Fatalf("reload failed: %v", err)
	}
	if err := startDnsmasq(cfg, runDir); err != nil {
		cleanup(cfg)
		log.Fatalf("reload failed: %v", err)
	}
	st.SSID = cfg.SSID
	writeState(st)
	logInfo("reloaded (ssid=%q channel=%d)", cfg.SSID, st.Channel)
}

func main() {
	log.SetFlags(log.LstdFlags)
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "routerd must run as root (e.g. sudo systemctl start routerd)")
		os.Exit(2)
	}

	args := os.Args[1:]
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-c", "--config":
			if len(args) < 2 {
				usage()
				os.Exit(2)
			}
			configPath = args[1]
			args = args[2:]
		case "-h", "--help":
			usage()
			os.Exit(0)
		default:
			usage()
			os.Exit(2)
		}
	}
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "start":
		cmdStart()
	case "stop":
		cmdStop()
	case "status":
		cmdStatus()
	case "reload":
		cmdReload()
	case "version":
		fmt.Printf("routerd %s\n", version)
	default:
		usage()
		os.Exit(2)
	}
}
