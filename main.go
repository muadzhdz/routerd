package main

import (
	"bufio"
	"context"
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

	// Detect STA interface and channel BEFORE cleanup so that cleanup's
	// deleteAPInterface (iw dev ap0 del) cannot disturb the phy state and
	// cause iw dev <sta> info to return empty output on some drivers.
	sta, err := findSTAInterface(cfg)
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

	cleanup(cfg)

	failed := true
	defer func() {
		if failed {
			cleanup(cfg)
		}
	}()

	// Item 7: context-based cancellation — replaces sig channel + signal.Notify.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	uplink, err := detectUplink(cfg, sta)
	if err != nil {
		logWarn("%v", err)
		cleanup(cfg)
		os.Exit(1)
	}
	if cfg.Uplink == "" || cfg.Uplink == "auto" {
		cfg.Uplink = uplink
	}

	activeUplink, err := startVPN(cfg, runDir)
	if err != nil {
		logWarn("VPN setup error: %v", err)
		cleanup(cfg)
		os.Exit(1)
	}

	cfg.Uplink = activeUplink

	if strings.EqualFold(cfg.Subnet, "random") {
		cfg.Subnet = generateRandomSubnet()
		logInfo("generated random client subnet: %s", cfg.Subnet)
	}

	gwCIDR, err := gatewayCIDR(cfg.Subnet)
	if err != nil {
		logWarn("%v", err)
		cleanup(cfg)
		os.Exit(1)
	}

	if err := createAPInterface(sta, cfg.InterfaceAP, cfg.RandomMAC); err != nil {
		logWarn("%v", err)
		cleanup(cfg)
		os.Exit(1)
	}
	if err := interfaceUp(cfg.InterfaceAP); err != nil {
		logWarn("%v", err)
		cleanup(cfg)
		os.Exit(1)
	}
	setUnmanaged(cfg.InterfaceAP)

	if err := startHostapd(cfg, ch, band, runDir); err != nil {
		logWarn("%v", err)
		cleanup(cfg)
		os.Exit(1)
	}
	if err := addrAdd(cfg.InterfaceAP, gwCIDR); err != nil {
		logWarn("%v", err)
		cleanup(cfg)
		os.Exit(1)
	}
	if err := startDnsmasq(cfg, runDir); err != nil {
		logWarn("%v", err)
		cleanup(cfg)
		os.Exit(1)
	}
	if err := enableNAT(NATConfig{
		RunDir:        runDir,
		Uplink:        activeUplink,
		AP:            cfg.InterfaceAP,
		IsolateHost:   cfg.IsolateHost,
		SpoofTTL:      cfg.SpoofTTL,
		TorMode:       cfg.TorMode,
		DisableIPv6:   cfg.DisableIPv6,
		LimitRateMbps: cfg.LimitRateMbps,
		EnableVPN:     cfg.EnableVPN,
		VPNKillSwitch: cfg.VPNKillSwitch,
		VPNDNS:        cfg.VPNDNS,
		DPIBypass:     cfg.DPIBypass,
	}); err != nil {
		logWarn("%v", err)
		cleanup(cfg)
		os.Exit(1)
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

	// Auto-start dashboard if enabled in config (passes ctx so it shuts down with the daemon).
	if cfg.DashboardEnabled {
		vpnConfPath := cfg.VPNConfig
		if vpnConfPath == "" {
			vpnConfPath = "/etc/routerd/vpn.conf"
		}
		dashSrv := dashboard.NewServer(
			configPath, vpnConfPath, runDir, version,
			cfg.DashboardBind, cfg.DashboardPassword, cfg.DashboardPort,
		)
		go func() {
			logInfo("dashboard auto-started on http://%s:%d", cfg.DashboardBind, cfg.DashboardPort)
			if err := dashSrv.Run(ctx); err != nil && err.Error() != "http: Server closed" {
				logWarn("dashboard: %v", err)
			}
		}()
	}

	// Watchdogs: cancel the context (graceful shutdown) if hostapd or dnsmasq crash.
	procMgr.watchdog("hostapd", func(name string) {
		logWarn("watchdog triggered cleanup due to %s crash", name)
		cancel()
	})
	procMgr.watchdog("dnsmasq", func(name string) {
		logWarn("watchdog triggered cleanup due to %s crash", name)
		cancel()
	})

	// Write clients.json periodically so the dashboard can read it without root.
	go func() {
		writeClients(cfg.InterfaceAP, runDir)
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				writeClients(cfg.InterfaceAP, runDir)
			}
		}
	}()

	<-ctx.Done()
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

// cmdReload performs a full atomic reload: restarts hostapd, dnsmasq, and
// re-applies all NAT rules so config changes (VPN, isolation, DNS, etc.) take
// effect. If any step fails after teardown, it attempts to restore the old
// state rather than calling full cleanup (which would destroy the AP interface
// and leak the routing setup into an unknown state).
func cmdReload() {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	st, ok := readState()
	if !ok {
		log.Fatalf("routerd is not running (no state found)")
	}

	// --- Snapshot old state for rollback ------------------------------------
	oldSt := st
	oldVPNStarted, _ := os.ReadFile(filepath.Join(runDir, "vpn_started"))

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

	// --- Teardown (point of no return) --------------------------------------
	stopProcs()
	killOrphans()
	disableNAT(runDir, st.InterfaceAP)

	// reloadFailed is called when a step after teardown fails. It attempts to
	// restore processes and NAT rules using the old (pre-reload) config so the
	// daemon stays functional. Full cleanup is intentionally NOT called here
	// because that would destroy the AP interface itself.
	reloadFailed := func(step string, stepErr error) {
		logWarn("reload failed at %s: %v — attempting restore with old config", step, stepErr)

		// Rebuild an old-compatible Config for hostapd/dnsmasq/NAT restart.
		oldCfg, cfgErr := LoadConfig(configPath)
		if cfgErr != nil {
			oldCfg = cfg // best effort: use new cfg as base
		}
		oldCfg.SSID = oldSt.SSID
		oldCfg.InterfaceAP = oldSt.InterfaceAP
		oldCfg.InterfaceSTA = oldSt.InterfaceSTA
		oldCfg.Subnet = oldSt.Subnet
		oldCfg.Channel = strconv.Itoa(oldSt.Channel)
		oldCfg.EnableVPN = oldSt.VPNActive
		oldCfg.VPNMode = oldSt.VPNMode

		// Restore vpn_started marker so stopVPN/startVPN work correctly.
		if len(oldVPNStarted) > 0 {
			_ = os.WriteFile(filepath.Join(runDir, "vpn_started"), oldVPNStarted, 0600)
		}

		restoreUplink, vpnErr := startVPN(oldCfg, runDir)
		if vpnErr != nil {
			logWarn("restore: VPN restart failed: %v", vpnErr)
			restoreUplink = oldSt.Uplink
		}
		oldCfg.Uplink = restoreUplink

		if hErr := startHostapd(oldCfg, oldSt.Channel, oldSt.Band, runDir); hErr != nil {
			logWarn("restore: hostapd restart failed: %v", hErr)
		}
		if gwCIDR, gErr := gatewayCIDR(oldSt.Subnet); gErr == nil {
			if aErr := addrAdd(oldSt.InterfaceAP, gwCIDR); aErr != nil {
				logWarn("restore: addrAdd failed: %v", aErr)
			}
		}
		if dErr := startDnsmasq(oldCfg, runDir); dErr != nil {
			logWarn("restore: dnsmasq restart failed: %v", dErr)
		}
		if nErr := enableNAT(NATConfig{
			RunDir:        runDir,
			Uplink:        restoreUplink,
			AP:            oldSt.InterfaceAP,
			IsolateHost:   oldCfg.IsolateHost,
			SpoofTTL:      oldCfg.SpoofTTL,
			TorMode:       oldCfg.TorMode,
			DisableIPv6:   oldCfg.DisableIPv6,
			LimitRateMbps: oldCfg.LimitRateMbps,
			EnableVPN:     oldCfg.EnableVPN,
			VPNKillSwitch: oldCfg.VPNKillSwitch,
			VPNDNS:        oldCfg.VPNDNS,
			DPIBypass:     oldCfg.DPIBypass,
		}); nErr != nil {
			logWarn("restore: NAT re-enable failed: %v", nErr)
		}
		writeState(oldSt)
		logWarn("restore complete — running with old config (ssid=%q channel=%d)", oldSt.SSID, oldSt.Channel)
	}

	// --- Bring up new config ------------------------------------------------
	cfg.InterfaceSTA = st.InterfaceSTA
	activeUplink, err := startVPN(cfg, runDir)
	if err != nil {
		reloadFailed("startVPN", err)
		return
	}
	cfg.Uplink = activeUplink

	if err := startHostapd(cfg, ch, band, runDir); err != nil {
		reloadFailed("startHostapd", err)
		return
	}

	gwCIDR, err := gatewayCIDR(cfg.Subnet)
	if err != nil {
		reloadFailed("gatewayCIDR", err)
		return
	}
	if err := addrAdd(cfg.InterfaceAP, gwCIDR); err != nil {
		reloadFailed("addrAdd", err)
		return
	}

	if err := startDnsmasq(cfg, runDir); err != nil {
		reloadFailed("startDnsmasq", err)
		return
	}

	// Re-apply all NAT rules with the (possibly updated) config.
	if err := enableNAT(NATConfig{
		RunDir:        runDir,
		Uplink:        activeUplink,
		AP:            cfg.InterfaceAP,
		IsolateHost:   cfg.IsolateHost,
		SpoofTTL:      cfg.SpoofTTL,
		TorMode:       cfg.TorMode,
		DisableIPv6:   cfg.DisableIPv6,
		LimitRateMbps: cfg.LimitRateMbps,
		EnableVPN:     cfg.EnableVPN,
		VPNKillSwitch: cfg.VPNKillSwitch,
		VPNDNS:        cfg.VPNDNS,
		DPIBypass:     cfg.DPIBypass,
	}); err != nil {
		reloadFailed("enableNAT", err)
		return
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

	// If the routerd daemon is already running with DASHBOARD_ENABLED=true,
	// the port is already bound. Inform the user instead of crashing.
	if cfg.DashboardEnabled {
		if st, ok := readState(); ok && hostapdRunning() {
			fmt.Printf("routerd daemon is running and the dashboard is already active at http://%s:%d\n", bind, port)
			fmt.Printf("  ssid=%s uplink=%s subnet=%s\n", st.SSID, st.Uplink, st.Subnet)
			fmt.Println("  To open a separate dashboard instance, set DASHBOARD_ENABLED=false in /etc/routerd.conf")
			fmt.Println("  and restart the service, then run 'routerd dashboard' again.")
			return
		}
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
