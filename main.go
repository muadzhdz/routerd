package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muadzhdz/routerd/pkg/config"
	"github.com/muadzhdz/routerd/pkg/dashboard"
	"github.com/muadzhdz/routerd/pkg/dns"
	"github.com/muadzhdz/routerd/pkg/engine"
	"github.com/muadzhdz/routerd/pkg/hotspot"
	"github.com/muadzhdz/routerd/pkg/ipc"
	"github.com/muadzhdz/routerd/pkg/telemetry"
	"github.com/muadzhdz/routerd/pkg/vpn"
)

var (
	// Version is the semver release tag, set at link time via -ldflags.
	Version = "dev"
	// GitCommit is the short commit hash, set at link time via -ldflags.
	GitCommit = "none"
	// BuildDate is the RFC3339 build timestamp, set at link time via -ldflags.
	BuildDate = "unknown"
)

func handleCLICommands(args []string) bool {
	if len(args) < 2 {
		return false
	}

	cmd := args[1]
	if cmd == "version" || cmd == "-v" || cmd == "--version" {
		fmt.Printf("routerd version %s (commit: %s, built: %s)\n", Version, GitCommit, BuildDate)
		return true
	}

	if cmd != "status" && cmd != "clients" && cmd != "ping" {
		return false
	}

	sockPath := ipc.GetDefaultSocketPath()
	if !ipc.IsDaemonRunning(sockPath) {
		fmt.Println("routerd daemon is not running.")
		fmt.Println("Start daemon with: sudo systemctl start routerd (or: sudo routerd -d -H)")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := ipc.NewClient(sockPath)
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to routerd daemon: %v", err)
	}
	defer client.Close()

	switch cmd {
	case "ping":
		if err := client.Ping(1 * time.Second); err != nil {
			log.Fatalf("Ping failed: %v", err)
		}
		fmt.Println("pong")
	case "status", "clients":
		resp, err := client.SendCommand(ctx, cmd)
		if err != nil {
			log.Fatalf("Command %s failed: %v", cmd, err)
		}
		fmt.Println(resp)
	}

	return true
}

// CLIOptions holds parsed command-line flags.
type CLIOptions struct {
	ConfigPath    string
	Interface     string
	Hotspot       bool
	SSID          string
	Password      string
	Headless      bool
	Attach        bool
	Version       bool
	VPN           bool
	ExplicitFlags map[string]bool
}

func printUsage() {
	fmt.Println("routerd - eBPF Stealth Router, DNS Resolver and Hotspot Daemon")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  routerd [command]")
	fmt.Println("  routerd [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  status               Show status of the running background daemon")
	fmt.Println("  clients              List active connected Wi-Fi clients")
	fmt.Println("  ping                 Probe daemon IPC responsiveness")
	fmt.Println("  version              Print routerd build and version information")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -c, --config PATH    Configuration file path (default: /etc/routerd/routerd.conf)")
	fmt.Println("  -i, --iface NAME     Target WAN interface (default: auto-detect default route)")
	fmt.Println("  -H, --hotspot        Enable Wi-Fi Access Point & Stealth NAT Router")
	fmt.Println("  -s, --ssid NAME      Wi-Fi Hotspot SSID name (default: from config or 'routerd')")
	fmt.Println("  -p, --password PASS  Wi-Fi Hotspot password (min 8 chars, default: from config)")
	fmt.Println("  -V, --vpn            Enable WireGuard VPN uplink & policy routing")
	fmt.Println("  -d, --headless       Run as background daemon without TUI Dashboard")
	fmt.Println("  -a, --attach         Attach TUI to running background daemon")
	fmt.Println("  -v, --version        Print routerd version and exit")
	fmt.Println("  -h, --help           Show this help message")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  sudo routerd -d -H                  # Run background daemon with Wi-Fi AP")
	fmt.Println("  routerd                             # Open TUI dashboard (auto-attaches to daemon)")
	fmt.Println("  routerd clients                     # Quick view of connected stations")
	fmt.Println("  sudo routerd -H -V                  # Hotspot + WireGuard VPN tunnel")
	fmt.Println("  sudo routerd -H -s 'lab' -p 'pass'  # Standalone foreground router with custom AP")
}

func parseCLIOptions(args []string) (*CLIOptions, error) {
	opts := &CLIOptions{
		ExplicitFlags: make(map[string]bool),
	}

	fs := flag.NewFlagSet("routerd", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = printUsage

	fs.StringVar(&opts.ConfigPath, "config", config.DefaultConfigFile, "Configuration file path")
	fs.StringVar(&opts.ConfigPath, "c", config.DefaultConfigFile, "Configuration file path (alias)")

	fs.StringVar(&opts.Interface, "iface", "", "Target WAN network interface")
	fs.StringVar(&opts.Interface, "i", "", "Target WAN network interface (alias)")

	fs.BoolVar(&opts.Hotspot, "hotspot", false, "Enable Wi-Fi Hotspot & Stealth NAT")
	fs.BoolVar(&opts.Hotspot, "H", false, "Enable Wi-Fi Hotspot & Stealth NAT (alias)")

	fs.StringVar(&opts.SSID, "ssid", "", "Wi-Fi Hotspot SSID name")
	fs.StringVar(&opts.SSID, "s", "", "Wi-Fi Hotspot SSID name (alias)")

	fs.StringVar(&opts.Password, "password", "", "Wi-Fi Hotspot password (min 8 characters)")
	fs.StringVar(&opts.Password, "p", "", "Wi-Fi Hotspot password (alias)")

	fs.BoolVar(&opts.VPN, "vpn", false, "Enable WireGuard VPN uplink")
	fs.BoolVar(&opts.VPN, "V", false, "Enable WireGuard VPN uplink (alias)")

	fs.BoolVar(&opts.Headless, "headless", false, "Run as background daemon without TUI")
	fs.BoolVar(&opts.Headless, "d", false, "Run as background daemon without TUI (alias)")

	fs.BoolVar(&opts.Attach, "attach", false, "Attach TUI to running background daemon")
	fs.BoolVar(&opts.Attach, "a", false, "Attach TUI to running background daemon (alias)")

	fs.BoolVar(&opts.Version, "version", false, "Print routerd version and exit")
	fs.BoolVar(&opts.Version, "v", false, "Print routerd version and exit (alias)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	fs.Visit(func(f *flag.Flag) {
		opts.ExplicitFlags[f.Name] = true
	})

	return opts, nil
}

func resolveConfig(opts *CLIOptions, fileCfg config.FileConfig) (finalIface string, finalHotspot bool, finalSSID string, finalPassword string, finalVPN bool) {
	finalIface = fileCfg.Interface
	if opts.ExplicitFlags["iface"] || opts.ExplicitFlags["i"] {
		finalIface = opts.Interface
	}

	finalHotspot = fileCfg.Hotspot
	if opts.ExplicitFlags["hotspot"] || opts.ExplicitFlags["H"] {
		finalHotspot = opts.Hotspot
	}

	finalSSID = fileCfg.SSID
	if opts.ExplicitFlags["ssid"] || opts.ExplicitFlags["s"] {
		finalSSID = opts.SSID
	}

	finalPassword = fileCfg.Password
	if opts.ExplicitFlags["password"] || opts.ExplicitFlags["p"] {
		finalPassword = opts.Password
	}

	finalVPN = fileCfg.VPNEnabled
	if opts.ExplicitFlags["vpn"] || opts.ExplicitFlags["V"] {
		finalVPN = opts.VPN
	}

	return finalIface, finalHotspot, finalSSID, finalPassword, finalVPN
}

func main() {
	// 0A. Handle CLI subcommands (status, clients, ping, version)
	if handleCLICommands(os.Args) {
		return
	}

	opts, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(1)
	}

	if opts.Version {
		fmt.Printf("routerd version %s (commit: %s, built: %s)\n", Version, GitCommit, BuildDate)
		return
	}

	sockPath := ipc.GetDefaultSocketPath()
	daemonActive := ipc.IsDaemonRunning(sockPath)

	// Check if user passed configuration flags
	hasConfigFlags := opts.ExplicitFlags["iface"] || opts.ExplicitFlags["i"] ||
		opts.ExplicitFlags["hotspot"] || opts.ExplicitFlags["H"] ||
		opts.ExplicitFlags["ssid"] || opts.ExplicitFlags["s"] ||
		opts.ExplicitFlags["password"] || opts.ExplicitFlags["p"] ||
		opts.ExplicitFlags["vpn"] || opts.ExplicitFlags["V"] ||
		opts.ExplicitFlags["config"] || opts.ExplicitFlags["c"]

	// 0B. If daemon is active in the background
	if daemonActive {
		if opts.Headless {
			fmt.Printf("Error: routerd daemon is already active in background (socket: %s).\n", sockPath)
			fmt.Println("To view status: routerd status")
			fmt.Println("To attach TUI:  routerd")
			return
		}

		if hasConfigFlags {
			fmt.Printf("Notice: routerd daemon is already active in the background.\n")
			fmt.Printf("CLI flags cannot reconfigure an active daemon on the fly.\n\n")
			fmt.Printf("To change settings:\n")
			fmt.Printf("  1. Edit /etc/routerd/routerd.conf\n")
			fmt.Printf("  2. Run: sudo systemctl restart routerd\n\n")
			fmt.Printf("To attach to the live TUI dashboard, run: routerd (or routerd -a)\n")
			return
		}

		// Attach to running daemon
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		client := ipc.NewClient(sockPath)
		if err := client.Connect(ctx); err != nil {
			log.Fatalf("Failed to attach to routerd daemon at %s: %v", sockPath, err)
		}
		defer client.Close()

		var statusInfo struct {
			WANIface      string `json:"wan_iface"`
			WANIP         string `json:"wan_ip"`
			HotspotActive bool   `json:"hotspot_active"`
			SSID          string `json:"ssid"`
			VPNActive     bool   `json:"vpn_active"`
			VPNIface      string `json:"vpn_iface"`
		}
		if rawStatus, err := client.SendCommand(ctx, "status_json"); err == nil {
			_ = json.Unmarshal([]byte(rawStatus), &statusInfo)
		}

		snapCh, dnsCh, err := client.Subscribe()
		if err != nil {
			log.Fatalf("Failed to subscribe to telemetry stream: %v", err)
		}

		dashCfg := dashboard.Config{
			WANIface:       statusInfo.WANIface,
			WANIP:          statusInfo.WANIP,
			HotspotActive:  statusInfo.HotspotActive,
			SSID:           statusInfo.SSID,
			VPNActive:      statusInfo.VPNActive,
			VPNIface:       statusInfo.VPNIface,
			SnapshotChan:   snapCh,
			DNSEventChan:   dnsCh,
			IsRemoteClient: true,
			ShutdownFunc:   func() { _ = client.Close() },
		}

		p := tea.NewProgram(dashboard.NewModel(dashCfg), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			log.Printf("Error running attached dashboard: %v", err)
		}
		fmt.Println("Detached from routerd daemon. Daemon is still running in background.")
		return
	}

	// 0C. If daemon is NOT active and user ran unprivileged 'routerd' with no flags
	if os.Geteuid() != 0 && !hasConfigFlags && !opts.Headless {
		fmt.Println("routerd daemon is not active.")
		fmt.Println()
		fmt.Println("To start in background via systemd:")
		fmt.Println("  sudo systemctl start routerd")
		fmt.Println()
		fmt.Println("To start standalone in foreground:")
		fmt.Println("  sudo routerd -H (or: sudo routerd --hotspot)")
		fmt.Println()
		fmt.Println("For full help: routerd --help")
		return
	}

	// Load configuration from file, falling back to defaults
	fileCfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		log.Printf("Warning: error reading config file %s: %v (using defaults)", opts.ConfigPath, err)
	}

	// Merge: CLI flags override config file
	finalIface, finalHotspot, finalSSID, finalPassword, finalVPN := resolveConfig(opts, fileCfg)

	// Trap termination signals (SIGINT, SIGTERM)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Initialize & Start eBPF Packet Engine (Deep Module)
	eng, err := engine.Start(engine.Config{
		InterfaceName: finalIface,
		XDPMode:       engine.XDPModeAuto,
	})
	if err != nil {
		log.Fatalf("Failed to start eBPF Engine: %v", err)
	}
	defer eng.Close()

	iface := eng.Interface()
	log.Printf("SUCCESS: eBPF Engine ACTIVE on interface [%s]!", iface.Name)

	// 2. Start Stealth DoH DNS Resolver Engine with In-Memory Cache, Ad-Blocker & Fallback
	dnsServer, err := dns.NewServer(dns.ServerConfig{
		ListenAddr:    "127.0.0.1:53",
		Upstreams:     fileCfg.Upstreams,
		BlockAds:      fileCfg.BlockAds,
		BlocklistPath: fileCfg.BlocklistFile,
	})
	if err != nil {
		log.Fatalf("Failed to initialize DNS Resolver: %v", err)
	}
	if err := dnsServer.Start(); err != nil {
		log.Printf("Warning: Failed to start DNS Server: %v", err)
	}
	defer dnsServer.Close()

	// Divert system DNS on target interface to 127.0.0.1 via systemd-resolved
	_ = exec.Command("resolvectl", "dns", iface.Name, "127.0.0.1").Run()
	_ = exec.Command("resolvectl", "flush-caches").Run()
	log.Printf("SUCCESS: System DNS [%s] redirected to 127.0.0.1 (DoH Cloudflare/Google)!", iface.Name)

	// Guarantee default DNS restoration on program exit
	defer func() {
		_ = exec.Command("resolvectl", "revert", iface.Name).Run()
		_ = exec.Command("resolvectl", "flush-caches").Run()
		log.Println("SUCCESS: DNS reverted to default DHCP.")
	}()

	// 3. If hotspot is active: Start Wi-Fi Access Point & Stealth NAT
	var hotspotCtrl *hotspot.Controller
	if finalHotspot {
		hotspotCtrl = hotspot.NewController(hotspot.Config{
			ParentIface: iface.Name,
			SSID:        finalSSID,
			Password:    finalPassword,
		})
		if err := hotspotCtrl.Start(); err != nil {
			log.Printf("Warning Hotspot: %v", err)
		} else {
			hotspot.SetDefaultController(hotspotCtrl)
			defer func() {
				hotspot.SetDefaultController(nil)
				_ = hotspotCtrl.Close()
			}()
		}
	}

	// 4. If VPN is enabled: Start WireGuard VPN uplink & apply policy routing
	var vpnCtrl *vpn.Controller
	vpnActive := false
	vpnIface := ""
	if finalVPN {
		vpnCtrl = vpn.NewController(vpn.Config{
			ProfilePath: fileCfg.VPNConfig,
			RunDir:      "/run/routerd",
			APInterface: "ap0",
		})
		if vi, err := vpnCtrl.Start(); err != nil {
			log.Printf("Warning VPN: %v (falling back to direct WAN uplink [%s])", err, iface.Name)
		} else {
			vpnActive = true
			vpnIface = vi
			defer vpnCtrl.Stop()
		}
	}

	// 5. Detect WAN IP
	var wanIP string
	if addrs, err := iface.Addrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				wanIP = ipNet.IP.String()
				break
			}
		}
	}

	// 6. Start Unix Domain Socket IPC Server
	ipcServer := ipc.NewServer(sockPath)
	ipcServer.RegisterCommandHandler("status", func(args []string) (string, error) {
		hotspotStr := "DISABLED"
		if finalHotspot {
			hotspotStr = fmt.Sprintf("ACTIVE (SSID: %s)", finalSSID)
		}
		vpnStr := "DISABLED"
		if vpnActive {
			vpnStr = fmt.Sprintf("ACTIVE (%s)", vpnIface)
		}
		return fmt.Sprintf("routerd active\nWAN: %s (%s)\nHotspot: %s\nVPN: %s", iface.Name, wanIP, hotspotStr, vpnStr), nil
	})
	ipcServer.RegisterCommandHandler("status_json", func(args []string) (string, error) {
		data, _ := json.Marshal(map[string]any{
			"wan_iface":      iface.Name,
			"wan_ip":         wanIP,
			"hotspot_active": finalHotspot,
			"ssid":           finalSSID,
			"vpn_active":     vpnActive,
			"vpn_iface":      vpnIface,
		})
		return string(data), nil
	})
	ipcServer.RegisterCommandHandler("clients", func(args []string) (string, error) {
		clients, err := hotspot.GetConnectedClients()
		if err != nil {
			return "", err
		}
		if len(clients) == 0 {
			return "No active wireless stations connected.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%-16s %-15s %-17s %-9s %-8s\n", "HOSTNAME", "IP ADDRESS", "MAC ADDRESS", "SIGNAL", "SPEED"))
		for _, c := range clients {
			sb.WriteString(fmt.Sprintf("%-16s %-15s %-17s %-9s %-8s\n", c.Hostname, c.IP, c.MAC, c.Signal, c.TxBitrate))
		}
		return strings.TrimSpace(sb.String()), nil
	})

	if err := ipcServer.Start(); err != nil {
		log.Printf("Warning: Failed to start IPC server: %v", err)
	} else {
		defer ipcServer.Close()
		log.Printf("SUCCESS: IPC Server ACTIVE on [%s]", ipcServer.SocketPath())
	}

	// 7. Run TUI Dashboard or Headless Daemon mode
	if opts.Headless {
		log.Println("=========================================================")
		log.Println("routerd DAEMON ACTIVE IN BACKGROUND (HEADLESS MODE)")
		log.Printf("WAN Interface : %s (%s)", iface.Name, wanIP)
		if finalHotspot {
			log.Printf("Hotspot AP    : SSID [%s] | Gateway 10.42.0.1 (ap0)", finalSSID)
			log.Println("Stealth NAT   : Port 53 redirected to DoH 127.0.0.1:53")
		}
		if vpnActive {
			log.Printf("VPN Uplink    : WireGuard ACTIVE on [%s] (Policy Routing Table 51820)", vpnIface)
		} else if finalVPN {
			log.Printf("VPN Uplink    : FAILED (Fallback to direct WAN uplink [%s])", iface.Name)
		} else {
			log.Println("VPN Uplink    : DISABLED (Direct WAN uplink)")
		}
		log.Printf("IPC Socket    : %s (Type 'routerd' to attach TUI)", ipcServer.SocketPath())
		log.Println("Waiting for termination signal (SIGTERM / Ctrl+C)...")
		log.Println("=========================================================")

		// Telemetry sampling loop to broadcast over IPC socket
		col := telemetry.NewCollector(telemetry.Config{
			WANIface:      iface.Name,
			HotspotActive: finalHotspot,
			StatsProvider: eng,
		})

		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					snap := col.Sample()
					ipcServer.BroadcastSnapshot(snap)
				}
			}
		}()

		// Broadcast DNS events to IPC clients
		go func() {
			for ev := range dnsServer.Events() {
				ipcServer.BroadcastDNSEvent(ev)
			}
		}()

		<-ctx.Done()
		log.Println("\nReceived termination signal. Initiating graceful shutdown...")
	} else {
		dashCfg := dashboard.Config{
			WANIface:      iface.Name,
			WANIP:         wanIP,
			HotspotActive: finalHotspot,
			SSID:          finalSSID,
			VPNActive:     vpnActive,
			VPNIface:      vpnIface,
			StatsProvider: eng,
			DNSEventChan:  dnsServer.Events(),
		}

		p := tea.NewProgram(dashboard.NewModel(dashCfg), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			log.Printf("Error running dashboard: %v", err)
		}
	}

	log.Println("Cleaning up Engine, VPN, Hotspot & Restoring DNS. Exiting cleanly!")
}
