package main

import (
	"testing"

	"github.com/muadzhdz/routerd/pkg/config"
)

func TestCLIFlagParsingShortAndLong(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		validate func(t *testing.T, opts *CLIOptions)
	}{
		{
			name: "Default Values",
			args: []string{},
			validate: func(t *testing.T, opts *CLIOptions) {
				if opts.ConfigPath != config.DefaultConfigFile {
					t.Errorf("expected default config %s, got %s", config.DefaultConfigFile, opts.ConfigPath)
				}
				if opts.Hotspot != false {
					t.Errorf("expected default hotspot false, got true")
				}
				if opts.Headless != false {
					t.Errorf("expected default headless false, got true")
				}
				if len(opts.ExplicitFlags) != 0 {
					t.Errorf("expected 0 explicit flags, got %d", len(opts.ExplicitFlags))
				}
			},
		},
		{
			name: "Long GNU Flags",
			args: []string{
				"--config", "/etc/custom/routerd.conf",
				"--iface", "eth0",
				"--hotspot",
				"--ssid", "alpha-net",
				"--password", "secretpass123",
				"--headless",
				"--attach",
			},
			validate: func(t *testing.T, opts *CLIOptions) {
				if opts.ConfigPath != "/etc/custom/routerd.conf" {
					t.Errorf("expected config /etc/custom/routerd.conf, got %s", opts.ConfigPath)
				}
				if opts.Interface != "eth0" {
					t.Errorf("expected iface eth0, got %s", opts.Interface)
				}
				if !opts.Hotspot {
					t.Errorf("expected hotspot true, got false")
				}
				if opts.SSID != "alpha-net" {
					t.Errorf("expected ssid alpha-net, got %s", opts.SSID)
				}
				if opts.Password != "secretpass123" {
					t.Errorf("expected password secretpass123, got %s", opts.Password)
				}
				if !opts.Headless {
					t.Errorf("expected headless true, got false")
				}
				if !opts.Attach {
					t.Errorf("expected attach true, got false")
				}
				if !opts.ExplicitFlags["config"] || !opts.ExplicitFlags["hotspot"] || !opts.ExplicitFlags["headless"] {
					t.Errorf("expected explicit flags recorded, got: %+v", opts.ExplicitFlags)
				}
			},
		},
		{
			name: "Short Alias Flags",
			args: []string{
				"-c", "/opt/routerd.conf",
				"-i", "wlan1",
				"-H",
				"-s", "bravo-net",
				"-p", "passphrase999",
				"-d",
				"-a",
			},
			validate: func(t *testing.T, opts *CLIOptions) {
				if opts.ConfigPath != "/opt/routerd.conf" {
					t.Errorf("expected config /opt/routerd.conf, got %s", opts.ConfigPath)
				}
				if opts.Interface != "wlan1" {
					t.Errorf("expected iface wlan1, got %s", opts.Interface)
				}
				if !opts.Hotspot {
					t.Errorf("expected hotspot true, got false")
				}
				if opts.SSID != "bravo-net" {
					t.Errorf("expected ssid bravo-net, got %s", opts.SSID)
				}
				if opts.Password != "passphrase999" {
					t.Errorf("expected password passphrase999, got %s", opts.Password)
				}
				if !opts.Headless {
					t.Errorf("expected headless true, got false")
				}
				if !opts.Attach {
					t.Errorf("expected attach true, got false")
				}
				if !opts.ExplicitFlags["c"] || !opts.ExplicitFlags["H"] || !opts.ExplicitFlags["d"] {
					t.Errorf("expected explicit short flags recorded, got: %+v", opts.ExplicitFlags)
				}
			},
		},
		{
			name: "Equal Sign Syntax",
			args: []string{
				"--ssid=delta-wifi",
				"--password=supersecret",
				"--hotspot=true",
			},
			validate: func(t *testing.T, opts *CLIOptions) {
				if opts.SSID != "delta-wifi" {
					t.Errorf("expected ssid delta-wifi, got %s", opts.SSID)
				}
				if opts.Password != "supersecret" {
					t.Errorf("expected password supersecret, got %s", opts.Password)
				}
				if !opts.Hotspot {
					t.Errorf("expected hotspot true, got false")
				}
			},
		},
		{
			name: "Hotspot False Explicit Override",
			args: []string{"--hotspot=false"},
			validate: func(t *testing.T, opts *CLIOptions) {
				if opts.Hotspot != false {
					t.Errorf("expected hotspot false, got true")
				}
				if !opts.ExplicitFlags["hotspot"] {
					t.Errorf("expected hotspot recorded in explicit flags")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseCLIOptions(tc.args)
			if err != nil {
				t.Fatalf("unexpected error parsing flags: %v", err)
			}
			tc.validate(t, opts)
		})
	}
}

func TestConfigPrecedence(t *testing.T) {
	fileCfg := config.FileConfig{
		Interface: "wlan0",
		Hotspot:   true,
		SSID:      "config-ssid",
		Password:  "config-pass",
	}

	t.Run("CLI Overrides Config File", func(t *testing.T) {
		opts, err := parseCLIOptions([]string{"-s", "cli-ssid", "-p", "cli-pass", "-i", "eth1"})
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}

		iface, hotspot, ssid, pass := resolveConfig(opts, fileCfg)
		if iface != "eth1" {
			t.Errorf("expected iface eth1, got %s", iface)
		}
		if !hotspot {
			t.Errorf("expected hotspot true from file, got false")
		}
		if ssid != "cli-ssid" {
			t.Errorf("expected ssid cli-ssid, got %s", ssid)
		}
		if pass != "cli-pass" {
			t.Errorf("expected password cli-pass, got %s", pass)
		}
	})

	t.Run("Config File Fallback When No CLI Flags Given", func(t *testing.T) {
		opts, err := parseCLIOptions([]string{"-d"})
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}

		iface, hotspot, ssid, pass := resolveConfig(opts, fileCfg)
		if iface != "wlan0" {
			t.Errorf("expected iface wlan0 from config, got %s", iface)
		}
		if !hotspot {
			t.Errorf("expected hotspot true from config, got false")
		}
		if ssid != "config-ssid" {
			t.Errorf("expected ssid config-ssid from config, got %s", ssid)
		}
		if pass != "config-pass" {
			t.Errorf("expected password config-pass from config, got %s", pass)
		}
	})

	t.Run("Explicit Hotspot Disable Overrides Config", func(t *testing.T) {
		opts, err := parseCLIOptions([]string{"--hotspot=false"})
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}

		_, hotspot, _, _ := resolveConfig(opts, fileCfg)
		if hotspot != false {
			t.Errorf("expected hotspot false, got true")
		}
	})
}
