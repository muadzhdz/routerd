package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// pidFile is the lock file that prevents multiple routerd instances.
const pidFile = "/run/routerd/routerd.pid"

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
	return os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0600)
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

// --- writeClients -----------------------------------------------------------

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
