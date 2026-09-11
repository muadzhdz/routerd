package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// DefaultConfigFile is the standard system-wide configuration path.
const DefaultConfigFile = "/etc/routerd/routerd.conf"

// FileConfig holds the persisted router settings.
type FileConfig struct {
	Interface     string
	Hotspot       bool
	SSID          string
	Password      string
	Upstreams     []string
	BlockAds      bool
	BlocklistFile string
}

// Default returns a FileConfig with sensible default parameters.
func Default() FileConfig {
	return FileConfig{
		Interface:     "",
		Hotspot:       false,
		SSID:          "routerd",
		Password:      "routerd123",
		Upstreams:     []string{"1.1.1.1", "8.8.8.8"},
		BlockAds:      true,
		BlocklistFile: "/etc/routerd/blocklist.txt",
	}
}

// Load reads and parses a key-value configuration file.
// If path is empty, it attempts to load /etc/routerd/routerd.conf.
// If the target file does not exist, it cleanly returns Default() without error.
func Load(path string) (FileConfig, error) {
	cfg := Default()
	if path == "" {
		path = DefaultConfigFile
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToUpper(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		// Strip surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}

		switch key {
		case "IFACE", "WAN_IFACE", "INTERFACE":
			if strings.ToLower(val) == "auto" {
				cfg.Interface = ""
			} else {
				cfg.Interface = val
			}
		case "HOTSPOT", "HOTSPOT_ENABLED":
			b, err := strconv.ParseBool(val)
			if err == nil {
				cfg.Hotspot = b
			}
		case "SSID", "WIFI_SSID":
			if val != "" {
				cfg.SSID = val
			}
		case "PASSWORD", "WIFI_PASSWORD", "PASS":
			if val != "" {
				cfg.Password = val
			}
		case "UPSTREAMS", "DNS_UPSTREAMS", "DNS":
			if val != "" {
				rawList := strings.Split(val, ",")
				var cleanList []string
				for _, item := range rawList {
					t := strings.TrimSpace(item)
					if t != "" {
						cleanList = append(cleanList, t)
					}
				}
				if len(cleanList) > 0 {
					cfg.Upstreams = cleanList
				}
			}
		case "BLOCK_ADS", "ADBLOCK", "BLOCKLIST_ENABLED":
			b, err := strconv.ParseBool(val)
			if err == nil {
				cfg.BlockAds = b
			}
		case "BLOCKLIST_FILE", "BLOCKLIST_PATH", "BLOCKLIST":
			if val != "" {
				cfg.BlocklistFile = val
			}
		}
	}

	return cfg, scanner.Err()
}
