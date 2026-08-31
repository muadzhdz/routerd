package main

import (
	"strings"
	"testing"
)

// --- hostapdConf -------------------------------------------------------------

func baseConfig() *Config {
	return &Config{
		SSID:        "TestAP",
		Password:    "testpass1",
		Channel:     "6",
		InterfaceAP: "ap0",
		Country:     "ID",
		MaxClients:  16,
		HideSSID:    false,
		WPA3:        false,
		DNS:         "127.0.0.53",
		VPNDNS:      "1.1.1.1",
		Subnet:      "192.168.50.0/24",
		EnableVPN:   false,
	}
}

func TestHostapdConfBasic(t *testing.T) {
	cfg := baseConfig()
	conf := hostapdConf(cfg, 6, "g")

	checks := []string{
		"interface=ap0",
		"ssid=TestAP",
		"hw_mode=g",
		"channel=6",
		"country_code=ID",
		"max_num_sta=16",
		"wpa=2",
		"wpa_passphrase=testpass1",
		"wpa_key_mgmt=WPA-PSK",
		"ieee80211n=1",
	}
	for _, want := range checks {
		if !strings.Contains(conf, want) {
			t.Errorf("hostapdConf missing %q\n--- conf ---\n%s", want, conf)
		}
	}
}

func TestHostapdConf5GHz(t *testing.T) {
	cfg := baseConfig()
	conf := hostapdConf(cfg, 36, "a")

	if !strings.Contains(conf, "hw_mode=a") {
		t.Error("expected hw_mode=a for 5GHz band")
	}
	if !strings.Contains(conf, "ieee80211ac=1") {
		t.Error("expected ieee80211ac=1 for 5GHz band")
	}
	if !strings.Contains(conf, "channel=36") {
		t.Error("expected channel=36")
	}
}

func TestHostapdConfOpenNetwork(t *testing.T) {
	cfg := baseConfig()
	cfg.Password = ""
	conf := hostapdConf(cfg, 6, "g")

	// Should not contain any auth/wpa directives.
	if strings.Contains(conf, "wpa=") {
		t.Error("open network should not have wpa= directive")
	}
	if strings.Contains(conf, "wpa_passphrase=") {
		t.Error("open network should not have wpa_passphrase= directive")
	}
}

func TestHostapdConfHiddenSSID(t *testing.T) {
	cfg := baseConfig()
	cfg.HideSSID = true
	conf := hostapdConf(cfg, 6, "g")

	if !strings.Contains(conf, "ignore_broadcast_ssid=1") {
		t.Error("hidden SSID should have ignore_broadcast_ssid=1")
	}
}

func TestHostapdConfWPA3(t *testing.T) {
	cfg := baseConfig()
	cfg.WPA3 = true
	conf := hostapdConf(cfg, 6, "g")

	if !strings.Contains(conf, "SAE") {
		t.Error("WPA3 conf should contain SAE in wpa_key_mgmt")
	}
	if !strings.Contains(conf, "sae_password=testpass1") {
		t.Error("WPA3 conf should have sae_password")
	}
	if !strings.Contains(conf, "ieee80211w=1") {
		t.Error("WPA3 conf should have ieee80211w=1")
	}
}

// --- dnsmasqConf -------------------------------------------------------------

func TestDnsmasqConfBasic(t *testing.T) {
	cfg := baseConfig()
	conf, err := dnsmasqConf(cfg)
	if err != nil {
		t.Fatalf("dnsmasqConf error: %v", err)
	}

	checks := []string{
		"port=53",
		"no-resolv",
		"interface=ap0",
		"bind-interfaces",
		"listen-address=192.168.50.1",
		"dhcp-range=192.168.50.10,192.168.50.254",
		"dhcp-option=option:router,192.168.50.1",
		"dhcp-option=option:dns-server,192.168.50.1",
		"dhcp-authoritative",
	}
	for _, want := range checks {
		if !strings.Contains(conf, want) {
			t.Errorf("dnsmasqConf missing %q\n--- conf ---\n%s", want, conf)
		}
	}
}

func TestDnsmasqConfVPNDNS(t *testing.T) {
	cfg := baseConfig()
	cfg.EnableVPN = true
	cfg.DNS = "127.0.0.53" // local stub — should be overridden
	cfg.VPNDNS = "10.64.0.1"

	conf, err := dnsmasqConf(cfg)
	if err != nil {
		t.Fatalf("dnsmasqConf error: %v", err)
	}
	if !strings.Contains(conf, "server=10.64.0.1") {
		t.Errorf("dnsmasqConf with VPN should use VPNDNS 10.64.0.1\n--- conf ---\n%s", conf)
	}
}

func TestDnsmasqConfCustomDNS(t *testing.T) {
	cfg := baseConfig()
	cfg.DNS = "8.8.8.8"
	cfg.EnableVPN = false

	conf, err := dnsmasqConf(cfg)
	if err != nil {
		t.Fatalf("dnsmasqConf error: %v", err)
	}
	if !strings.Contains(conf, "server=8.8.8.8") {
		t.Errorf("expected server=8.8.8.8 in dnsmasq conf\n--- conf ---\n%s", conf)
	}
}

func TestDnsmasqConfInvalidSubnet(t *testing.T) {
	cfg := baseConfig()
	cfg.Subnet = "not-a-subnet"
	_, err := dnsmasqConf(cfg)
	if err == nil {
		t.Error("expected error for invalid subnet, got nil")
	}
}

// --- dhcpRange ---------------------------------------------------------------

func TestDhcpRangeNonOverlap(t *testing.T) {
	start, end, err := dhcpRange("192.168.50.0/24")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// start should be > .1 (gateway)
	if start[3] <= 1 {
		t.Errorf("DHCP start %s should be above .1", start)
	}
	// end should be < .255 (broadcast)
	if end[3] == 255 {
		t.Errorf("DHCP end %s should not be broadcast address", end)
	}
	// start < end
	for i := 0; i < 4; i++ {
		if start[i] < end[i] {
			return
		}
	}
	t.Errorf("DHCP start %s should be less than end %s", start, end)
}
