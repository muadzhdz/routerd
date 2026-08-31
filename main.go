package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"routerd/dashboard"
)

const version = "0.1.0"
const runDir = "/run/routerd"

// pidFile is the lock file that prevents multiple routerd instances.
const pidFile = "/run/routerd/routerd.pid"

var configPath = defaultConfigPath

func logInfo(format string, a ...any) { log.Printf("[info] "+format, a...) }
func logWarn(format string, a ...any) { log.Printf("[warn] "+format, a...) }

func usage() {
	fmt.Println(`routerd - turn a Linux machine into a WiFi access point / router.

Usage:
  routerd [options] <command>

Commands:
  start       Bring up the access point, NAT routing, and VPN (foreground).
  stop        Tear everything down (interfaces, DHCP, NAT rules, VPN).
  status      Show the current state of the access point & VPN.
  reload      Regenerate hostapd/dnsmasq configs and restart them (full reload).
  logs        Tail the hostapd and dnsmasq log files.
  dashboard   Start the web dashboard server (default port: 8080).
  warp-setup  Generate a Cloudflare WARP WireGuard configuration template.
  version     Print the version.

Options:
  -c, --config PATH   Config file (default /etc/routerd.conf).
  -h, --help          Show this help.

Examples:
  sudo systemctl start routerd
  sudo routerd status
  sudo routerd logs
  sudo routerd dashboard
  sudo routerd warp-setup
  sudo routerd -c /etc/routerd.conf start`)
}

// --- PID lock ---------------------------------------------------------------

// acquirePIDLock creates the PID file. Returns an error if another instance is
// already running.
func acquirePIDLock() error {
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return err
	}
	// Check if stale PID file exists from a previously crashed instance.
	if data, err := os.ReadFile(pidFile); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 {
			// Check if the process is actually alive.
			if proc, err := os.FindProcess(pid); err == nil {
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					return fmt.Errorf("routerd is already running (pid %d). Run 'routerd stop' first", pid)
				}
			}
			// Stale PID file — remove it.
			logWarn("removing stale PID file (pid %d no longer running)", pid)
			_ = os.Remove(pidFile)
		}
	}
	return os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func releasePIDLock() {
	_ = os.Remove(pidFile)
}

// --- State persistence ------------------------------------------------------

// State holds the runtime state written to disk so other commands (status,
// reload, stop) can read it.
type State struct {
	SSID         string
	InterfaceAP  string
	InterfaceSTA string
	Uplink       string
	Channel      int
	Band         string
	Subnet       string
	VPNMode      string
	VPNActive    bool
	StartTime    int64 // Unix timestamp of daemon start
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
	fmt.Fprintf(&b, "VPN_MODE=%s\n", s.VPNMode)
	fmt.Fprintf(&b, "VPN_ACTIVE=%t\n", s.VPNActive)
	fmt.Fprintf(&b, "START_TIME=%d\n", s.StartTime)
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
		case "VPN_MODE":
			s.VPNMode = val
		case "VPN_ACTIVE":
			s.VPNActive = parseBool(val)
		case "START_TIME":
			s.StartTime, _ = strconv.ParseInt(val, 10, 64)
		}
	}
	return s, s.InterfaceAP != ""
}

// --- Process health ---------------------------------------------------------

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

func killOrphans() {
	_, _ = runCmd("pkill", "-f", "hostapd .*/routerd/hostapd.conf")
	_, _ = runCmd("pkill", "-f", "dnsmasq .*/routerd/dnsmasq.conf")
}

// --- Cleanup ----------------------------------------------------------------

func cleanup(cfg *Config) {
	stopProcs()
	killOrphans()
	if cfg != nil {
		stopVPN(cfg, runDir)
		disableNAT(runDir, cfg.InterfaceAP)
		if gwCIDR, err := gatewayCIDR(cfg.Subnet); err == nil {
			addrDel(cfg.InterfaceAP, gwCIDR)
		}
		deleteAPInterface(cfg.InterfaceAP)
	}
	_ = os.Remove(filepath.Join(runDir, "state"))
	_ = os.Remove(filepath.Join(runDir, "ip_forward.orig"))
	for _, f := range []string{
		"hostapd.conf", "dnsmasq.conf", "hostapd.pid", "dnsmasq.pid",
		"hostapd.log", "dnsmasq.log", "vpn_started",
		"rp_filter_ap.orig", "rp_filter_all.orig",
	} {
		_ = os.Remove(filepath.Join(runDir, f))
	}
}

// --- Commands ---------------------------------------------------------------

func cmdStart() {
	if err := acquirePIDLock(); err != nil {
		log.Fatalf("%v", err)
	}
	defer releasePIDLock()

	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := os.MkdirAll(runDir, 0755); err != nil {
		log.Fatalf("cannot create %s: %v", runDir, err)
	}

	cleanup(cfg)

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
	if cfg.Uplink == "" || cfg.Uplink == "auto" {
		cfg.Uplink = uplink
	}

	activeUplink, err := startVPN(cfg, runDir)
	if err != nil {
		log.Fatalf("VPN setup error: %v", err)
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
	cfg.Uplink = activeUplink

	if strings.EqualFold(cfg.Subnet, "random") {
		cfg.Subnet = generateRandomSubnet()
		logInfo("generated random client subnet: %s", cfg.Subnet)
	}

	gwCIDR, err := gatewayCIDR(cfg.Subnet)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if err := createAPInterface(sta, cfg.InterfaceAP, cfg.RandomMAC); err != nil {
		log.Fatalf("%v", err)
	}
	if err := interfaceUp(cfg.InterfaceAP); err != nil {
		log.Fatalf("%v", err)
	}
	setUnmanaged(cfg.InterfaceAP)

	if err := startHostapd(cfg, ch, band, runDir); err != nil {
		log.Fatalf("%v", err)
	}
	if err := addrAdd(cfg.InterfaceAP, gwCIDR); err != nil {
		log.Fatalf("%v", err)
	}
	if err := startDnsmasq(cfg, runDir); err != nil {
		log.Fatalf("%v", err)
	}
	if err := enableNAT(runDir, activeUplink, cfg.InterfaceAP,
		cfg.IsolateHost, cfg.SpoofTTL, cfg.TorMode, cfg.DisableIPv6,
		cfg.LimitRateMbps, cfg.EnableVPN, cfg.VPNKillSwitch, cfg.VPNDNS, cfg.DPIBypass); err != nil {
		log.Fatalf("%v", err)
	}

	writeState(State{
		SSID: cfg.SSID, InterfaceAP: cfg.InterfaceAP, InterfaceSTA: sta,
		Uplink: activeUplink, Channel: ch, Band: band, Subnet: cfg.Subnet,
		VPNMode: cfg.VPNMode, VPNActive: cfg.EnableVPN,
		StartTime: timeNow(),
	})
	failed = false

	logInfo("routerd running: ssid=%q channel=%d band=%s ap=%s sta=%s uplink=%s subnet=%s clients=%d random_mac=%t isolate_host=%t vpn_active=%t vpn_mode=%s kill_switch=%t vpn_dns=%s",
		cfg.SSID, ch, band, cfg.InterfaceAP, sta, activeUplink, cfg.Subnet, cfg.MaxClients,
		cfg.RandomMAC, cfg.IsolateHost, cfg.EnableVPN, cfg.VPNMode, cfg.VPNKillSwitch, cfg.VPNDNS)

	// Start watchdogs that trigger a graceful shutdown if hostapd or dnsmasq crash.
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	procMgr.watchdog("hostapd", func(name string) {
		logWarn("watchdog triggered cleanup due to %s crash", name)
		sig <- syscall.SIGTERM
	})
	procMgr.watchdog("dnsmasq", func(name string) {
		logWarn("watchdog triggered cleanup due to %s crash", name)
		sig <- syscall.SIGTERM
	})

	// Write clients.json periodically so the dashboard can read it without root.
	go func() {
		writeClients(cfg.InterfaceAP, runDir)
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-sig:
				return
			case <-t.C:
				writeClients(cfg.InterfaceAP, runDir)
			}
		}
	}()

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
	releasePIDLock()
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

	fmt.Println("routerd is running:")
	fmt.Printf("  SSID:        %s\n", st.SSID)
	fmt.Printf("  Channel:     %d (%s band)\n", st.Channel, st.Band)
	fmt.Printf("  AP iface:    %s (%s)\n", st.InterfaceAP, st.Subnet)
	fmt.Printf("  STA iface:   %s\n", st.InterfaceSTA)
	fmt.Printf("  Uplink:      %s\n", st.Uplink)

	// Uptime.
	if st.StartTime > 0 {
		uptime := uptimeSince(st.StartTime)
		fmt.Printf("  Uptime:      %s\n", uptime)
	}

	// VPN status with endpoint if available.
	if st.VPNActive {
		fmt.Printf("  VPN Status:  ACTIVE (Mode: %s)\n", st.VPNMode)
		if endpoint := vpnEndpoint(st.Uplink); endpoint != "" {
			fmt.Printf("  VPN Endpoint:%s\n", endpoint)
		}
	} else {
		fmt.Println("  VPN Status:  Disabled")
	}

	// Connected clients with MAC + IP.
	clients := listStations(st.InterfaceAP, runDir)
	if len(clients) == 0 {
		fmt.Println("  Clients:     0")
	} else {
		fmt.Printf("  Clients:     %d\n", len(clients))
		for _, c := range clients {
			fmt.Printf("    %-20s %s\n", c.MAC, c.IP)
		}
	}
}

// cmdReload performs a full reload: restarts hostapd, dnsmasq, and re-applies
// all NAT rules so config changes (VPN, isolation, DNS, etc.) take effect.
func cmdReload() {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	st, ok := readState()
	if !ok {
		log.Fatalf("routerd is not running (no state found)")
	}

	ch, band := st.Channel, st.Band
	if cfg.Channel == "" || cfg.Channel == "auto" {
		if ch, band, err = detectChannel(st.InterfaceSTA); err != nil {
			log.Fatalf("%v", err)
		}
	} else if n, err2 := strconv.Atoi(cfg.Channel); err2 == nil && n >= 1 && n <= 165 {
		ch = n
		if ch <= 14 {
			band = "g"
		} else {
			band = "a"
		}
	}

	// Handle subnet change: remove old gateway address first.
	if cfg.Subnet != st.Subnet {
		if oldCIDR, err := gatewayCIDR(st.Subnet); err == nil {
			addrDel(st.InterfaceAP, oldCIDR)
		}
	}

	// Tear down existing processes and NAT rules fully.
	stopProcs()
	killOrphans()
	disableNAT(runDir, st.InterfaceAP)

	// Re-apply VPN (may have changed).
	cfg.InterfaceSTA = st.InterfaceSTA
	activeUplink, err := startVPN(cfg, runDir)
	if err != nil {
		log.Fatalf("reload VPN error: %v", err)
	}
	cfg.Uplink = activeUplink

	if err := startHostapd(cfg, ch, band, runDir); err != nil {
		cleanup(cfg)
		log.Fatalf("reload failed: %v", err)
	}

	if gwCIDR, err := gatewayCIDR(cfg.Subnet); err != nil {
		cleanup(cfg)
		log.Fatalf("reload failed: %v", err)
	} else if err := addrAdd(cfg.InterfaceAP, gwCIDR); err != nil {
		cleanup(cfg)
		log.Fatalf("reload failed: %v", err)
	}

	if err := startDnsmasq(cfg, runDir); err != nil {
		cleanup(cfg)
		log.Fatalf("reload failed: %v", err)
	}

	// Re-apply all NAT rules with the (possibly updated) config.
	if err := enableNAT(runDir, activeUplink, cfg.InterfaceAP,
		cfg.IsolateHost, cfg.SpoofTTL, cfg.TorMode, cfg.DisableIPv6,
		cfg.LimitRateMbps, cfg.EnableVPN, cfg.VPNKillSwitch, cfg.VPNDNS, cfg.DPIBypass); err != nil {
		cleanup(cfg)
		log.Fatalf("reload NAT failed: %v", err)
	}

	st.SSID = cfg.SSID
	st.InterfaceAP = cfg.InterfaceAP
	st.Subnet = cfg.Subnet
	st.Channel = ch
	st.Band = band
	st.VPNActive = cfg.EnableVPN
	st.VPNMode = cfg.VPNMode
	st.Uplink = activeUplink
	writeState(st)
	logInfo("reloaded (ssid=%q channel=%d vpn=%t)", cfg.SSID, ch, cfg.EnableVPN)
}

// cmdLogs tails both hostapd and dnsmasq log files to stdout simultaneously.
func cmdLogs() {
	hostapdLog := filepath.Join(runDir, "hostapd.log")
	dnsmasqLog := filepath.Join(runDir, "dnsmasq.log")

	tailFile := func(path, prefix string) {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] cannot open %s: %v\n", prefix, path, err)
			return
		}
		defer f.Close()
		// Print existing content first.
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fmt.Printf("[%s] %s\n", prefix, sc.Text())
		}
		// Follow new lines — poll with a small sleep to avoid busy-spinning.
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				for _, l := range strings.Split(strings.TrimRight(string(buf[:n]), "\n"), "\n") {
					if l != "" {
						fmt.Printf("[%s] %s\n", prefix, l)
					}
				}
			}
			if err != nil {
				if err == io.EOF {
					time.Sleep(250 * time.Millisecond)
					continue
				}
				return
			}
		}
	}

	go tailFile(hostapdLog, "hostapd")
	tailFile(dnsmasqLog, "dnsmasq")
}

// writeClients writes the current connected client list to /run/routerd/clients.json
// so the dashboard can read it without needing root/iw access.
func writeClients(ap, runDir string) {
	type clientEntry struct {
		MAC      string `json:"mac"`
		IP       string `json:"ip"`
		Hostname string `json:"hostname"`
		Signal   int    `json:"signal"`
		TxRate   string `json:"tx_rate"`
	}

	out, err := runCmd("iw", "dev", ap, "station", "dump")
	if err != nil {
		return
	}

	// Read leases for IP resolution.
	leases := make(map[string]string)
	hostnames := make(map[string]string)
	for _, path := range []string{
		filepath.Join(runDir, "dnsmasq.leases"),
		"/var/lib/misc/dnsmasq.leases",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) >= 3 {
				mac := strings.ToLower(f[1])
				leases[mac] = f[2]
				if len(f) >= 4 && f[3] != "*" {
					hostnames[mac] = f[3]
				}
			}
		}
		break
	}

	var clients []clientEntry
	var cur *clientEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Station ") {
			if cur != nil {
				clients = append(clients, *cur)
			}
			parts := strings.Fields(line)
			mac := ""
			if len(parts) >= 2 {
				mac = strings.ToLower(parts[1])
			}
			ip := leases[mac]
			if ip == "" {
				ip = "(no lease)"
			}
			cur = &clientEntry{MAC: mac, IP: ip, Hostname: hostnames[mac]}
		} else if cur != nil {
			if strings.HasPrefix(line, "tx bitrate:") {
				cur.TxRate = strings.TrimSpace(strings.TrimPrefix(line, "tx bitrate:"))
			}
			if strings.HasPrefix(line, "signal:") {
				var sig int
				fmt.Sscan(strings.TrimSpace(strings.TrimPrefix(line, "signal:")), &sig)
				cur.Signal = sig
			}
		}
	}
	if cur != nil {
		clients = append(clients, *cur)
	}
	if clients == nil {
		clients = []clientEntry{}
	}

	b, err := json.Marshal(clients)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(runDir, "clients.json"), b, 0600)
}

func cmdDashboard() {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	bind := cfg.DashboardBind
	port := cfg.DashboardPort
	password := cfg.DashboardPassword
	vpnConf := cfg.VPNConfig
	if vpnConf == "" {
		vpnConf = "/etc/routerd/vpn.conf"
	}

	srv := dashboard.NewServer(configPath, vpnConf, runDir, version, bind, password, port)

	fmt.Printf("routerd dashboard starting on http://%s:%d\n", bind, port)
	if password == "" {
		fmt.Println("  auth: disabled (set DASHBOARD_PASSWORD in config to enable)")
	} else {
		fmt.Println("  auth: basic auth enabled")
	}
	fmt.Println("  press Ctrl+C to stop")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if err := srv.Run(ctx); err != nil && err.Error() != "http: Server closed" {
		log.Fatalf("dashboard: %v", err)
	}
	fmt.Println("dashboard stopped")
}

func cmdWarpSetup() {
	target := "/etc/routerd/vpn.conf"
	logInfo("generating Cloudflare WARP profile template at %s", target)
	if err := generateWARPConfig(target); err != nil {
		log.Fatalf("cannot generate WARP config: %v", err)
	}
	fmt.Printf("Generated WARP WireGuard profile template at %s\n", target)
	fmt.Println("Fill in your WireGuard keys/endpoints in /etc/routerd/vpn.conf and set ENABLE_VPN=true in /etc/routerd.conf")
}

// --- Client list helpers ----------------------------------------------------

// Client holds a connected station's MAC and IP address.
type Client struct {
	MAC string
	IP  string
}

// listStations returns connected clients from iw + dnsmasq lease file.
func listStations(ap, runDir string) []Client {
	out, err := runCmd("iw", "dev", ap, "station", "dump")
	if err != nil {
		return nil
	}
	// Build MAC list from station dump.
	var macs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Station ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				macs = append(macs, strings.ToLower(parts[1]))
			}
		}
	}
	if len(macs) == 0 {
		return nil
	}

	// Build MAC→IP map from dnsmasq leases.
	leaseMap := parseDnsmasqLeases(runDir)

	clients := make([]Client, 0, len(macs))
	for _, mac := range macs {
		ip := leaseMap[mac]
		if ip == "" {
			ip = "(no lease)"
		}
		clients = append(clients, Client{MAC: mac, IP: ip})
	}
	return clients
}

// parseDnsmasqLeases reads /run/routerd/dnsmasq.leases (if present) and
// returns a map of lowercase MAC → IP.
func parseDnsmasqLeases(runDir string) map[string]string {
	m := make(map[string]string)
	data, err := os.ReadFile(filepath.Join(runDir, "dnsmasq.leases"))
	if err != nil {
		// Try default dnsmasq lease path.
		data, err = os.ReadFile("/var/lib/misc/dnsmasq.leases")
		if err != nil {
			return m
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			mac := strings.ToLower(fields[1])
			ip := fields[2]
			m[mac] = ip
		}
	}
	return m
}

// vpnEndpoint tries to read the WireGuard endpoint from `wg show`.
func vpnEndpoint(iface string) string {
	out, err := runCmd("wg", "show", iface, "endpoints")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] != "(none)" {
			return " " + fields[1]
		}
	}
	return ""
}

// uptimeSince formats duration since a Unix timestamp as "Xh Ym Zs".
func uptimeSince(startUnix int64) string {
	secs := time.Now().Unix() - startUnix
	if secs < 0 {
		secs = 0
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func timeNow() int64 {
	return time.Now().Unix()
}

// --- Entry point ------------------------------------------------------------

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
	case "logs":
		cmdLogs()
	case "dashboard":
		cmdDashboard()
	case "warp-setup":
		cmdWarpSetup()
	case "version":
		fmt.Printf("routerd %s\n", version)
	default:
		usage()
		os.Exit(2)
	}
}
