package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	cfg, err := Load("/nonexistent/routerd-path.conf")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if cfg.SSID != "routerd" || cfg.Password != "routerd123" {
		t.Errorf("expected default SSID and password, got: %+v", cfg)
	}
}

func TestLoadValidConfigFile(t *testing.T) {
	content := `# Routerd Configuration
WIFI_SSID="muadz-cyber"
WIFI_PASSWORD='kopi-hitam-123'
WAN_IFACE=wlp2s0
HOTSPOT_ENABLED=true
DNS_UPSTREAMS=1.1.1.1, 8.8.8.8, 9.9.9.9
BLOCK_ADS=true
BLOCKLIST_FILE=/etc/routerd/custom-blocklist.txt
`
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "routerd.conf")
	if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := Load(confPath)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if cfg.SSID != "muadz-cyber" {
		t.Errorf("expected SSID 'muadz-cyber', got '%s'", cfg.SSID)
	}
	if cfg.Password != "kopi-hitam-123" {
		t.Errorf("expected Password 'kopi-hitam-123', got '%s'", cfg.Password)
	}
	if cfg.Interface != "wlp2s0" {
		t.Errorf("expected Interface 'wlp2s0', got '%s'", cfg.Interface)
	}
	if !cfg.Hotspot {
		t.Errorf("expected Hotspot true, got false")
	}
	if len(cfg.Upstreams) != 3 || cfg.Upstreams[2] != "9.9.9.9" {
		t.Errorf("unexpected upstreams: %v", cfg.Upstreams)
	}
	if !cfg.BlockAds {
		t.Errorf("expected BlockAds true, got false")
	}
	if cfg.BlocklistFile != "/etc/routerd/custom-blocklist.txt" {
		t.Errorf("expected BlocklistFile /etc/routerd/custom-blocklist.txt, got '%s'", cfg.BlocklistFile)
	}
}

func TestLoadAutoInterface(t *testing.T) {
	content := `IFACE=auto`
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "routerd.conf")
	_ = os.WriteFile(confPath, []byte(content), 0644)

	cfg, err := Load(confPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Interface != "" {
		t.Errorf("expected empty string for auto interface, got '%s'", cfg.Interface)
	}
}
