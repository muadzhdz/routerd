package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const defaultConfigPath = "/etc/routerd.conf"

// Config holds the runtime configuration of routerd.
type Config struct {
	SSID          string
	Password      string
	Channel       string // "auto" or a channel number
	InterfaceSTA  string // wireless client interface (uplink wifi); "auto"
	InterfaceAP   string // virtual AP interface
	Uplink        string // NAT uplink interface; "auto"
	Subnet        string // client CIDR or "random"
	Country       string
	MaxClients    int
	DNS           string
	RandomMAC     bool // generate cryptographically random MAC for AP interface
	IsolateHost   bool // block connected AP clients from accessing local host services
	SpoofTTL      int  // set outgoing TTL (e.g. 64) to hide tethering, 0 to disable
	TorMode       bool // transparently proxy TCP & DNS traffic via Tor
	DisableIPv6   bool   // disable IPv6 on AP interface & block IPv6 traffic to prevent leaks
	HideSSID      bool   // hide SSID broadcast (hidden AP network)
	LimitRateMbps int    // rate limit bandwidth on AP interface in Mbps, 0 to disable
	EnableVPN     bool   // enable automatic VPN routing for all connected AP clients
	VPNMode       string // "wireguard", "warp", "custom", or "dpibypass"
	VPNConfig     string // path to WireGuard profile (.conf)
	VPNInterface  string // VPN interface name (e.g. wg0, tun0)
	VPNKillSwitch bool   // block client traffic if VPN connection fails/drops
}

func DefaultConfig() *Config {
	return &Config{
		SSID:          "routerd",
		Password:      "",
		Channel:       "auto",
		InterfaceSTA:  "auto",
		InterfaceAP:   "ap0",
		Uplink:        "auto",
		Subnet:        "192.168.50.0/24",
		Country:       "ID",
		MaxClients:    16,
		DNS:           "127.0.0.53",
		RandomMAC:     true,
		IsolateHost:   true,
		SpoofTTL:      64,
		TorMode:       false,
		DisableIPv6:   true,
		HideSSID:      false,
		LimitRateMbps: 0,
		EnableVPN:     false,
		VPNMode:       "wireguard",
		VPNConfig:     "/etc/routerd/vpn.conf",
		VPNInterface:  "wg0",
		VPNKillSwitch: true,
	}
}

func parseBool(s string) bool {
	v := strings.ToLower(strings.TrimSpace(s))
	return v == "true" || v == "1" || v == "yes" || v == "on"
}

// LoadConfig reads the KEY=VALUE config file. Missing files fall back to
// defaults; unknown keys are ignored.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = defaultConfigPath
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch key {
		case "SSID":
			cfg.SSID = val
		case "PASSWORD":
			cfg.Password = val
		case "CHANNEL":
			cfg.Channel = val
		case "INTERFACE_STA":
			cfg.InterfaceSTA = val
		case "INTERFACE_AP":
			cfg.InterfaceAP = val
		case "UPLINK":
			cfg.Uplink = val
		case "SUBNET":
			cfg.Subnet = val
		case "COUNTRY":
			cfg.Country = strings.ToUpper(val)
		case "MAX_CLIENTS":
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				cfg.MaxClients = n
			}
		case "DNS":
			cfg.DNS = val
		case "RANDOM_MAC":
			cfg.RandomMAC = parseBool(val)
		case "ISOLATE_HOST":
			cfg.IsolateHost = parseBool(val)
		case "SPOOF_TTL":
			if n, err := strconv.Atoi(val); err == nil && n >= 0 {
				cfg.SpoofTTL = n
			}
		case "TOR_MODE":
			cfg.TorMode = parseBool(val)
		case "DISABLE_IPV6":
			cfg.DisableIPv6 = parseBool(val)
		case "HIDE_SSID":
			cfg.HideSSID = parseBool(val)
		case "LIMIT_RATE_MBPS":
			if n, err := strconv.Atoi(val); err == nil && n >= 0 {
				cfg.LimitRateMbps = n
			}
		case "ENABLE_VPN":
			cfg.EnableVPN = parseBool(val)
		case "VPN_MODE":
			cfg.VPNMode = strings.ToLower(val)
		case "VPN_CONFIG":
			cfg.VPNConfig = val
		case "VPN_INTERFACE":
			cfg.VPNInterface = val
		case "VPN_KILL_SWITCH":
			cfg.VPNKillSwitch = parseBool(val)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(cfg.Password) != 0 && (len(cfg.Password) < 8 || len(cfg.Password) > 63) {
		return nil, fmt.Errorf("password must be 8-63 characters (got %d)", len(cfg.Password))
	}
	if cfg.SSID == "" {
		return nil, fmt.Errorf("ssid must not be empty")
	}
	return cfg, nil
}
