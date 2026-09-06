<div align="center">

<h1>routerd</h1>

<p>Turn any Linux machine with a Wi-Fi card into a <b>Stealth Wi-Fi AP + Router + Transparent WireGuard VPN Gateway</b></p>

<p>
  <a href="https://github.com/muadzhdz/routerd/actions/workflows/ci.yml"><img src="https://github.com/muadzhdz/routerd/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <img src="https://img.shields.io/badge/coverage-45.9%25-brightgreen" alt="Coverage">
  <img src="https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go" alt="Go">
  <img src="https://img.shields.io/badge/license-MIT-blue" alt="License">
</p>

</div>

---

Nearby devices instantly see a network named `routerd` and get internet access through your machine's existing Wi-Fi - no extra hardware. All client traffic is transparently routed through a **WireGuard / Cloudflare WARP VPN tunnel** with zero client-side configuration.

- **Single binary** - written in Go, zero runtime dependencies
- **Unified daemon** - `hostapd`, `dnsmasq`, `iptables`, `wg-quick` with clean lifecycle, watchdog, and atomic reload with rollback
- **Transparent VPN gateway** - WireGuard / WARP (auto-registered via `wgcf`) / custom interface / DPI bypass
- **Stealth engine** - random MAC, random subnet, TTL spoofing, IPv6 leak protection, host isolation
- **Web dashboard** - Cyber-Dark Glassmorphism SPA with live status, rolling bandwidth graphs, log terminal, QR code connect, and config editor
- **Unit tests**, race-detector clean, comprehensive coverage

---

## Quick Start

```sh
git clone https://github.com/muadzhdz/routerd && cd routerd

sudo ./install.sh --with-deps --enable              # plain AP (direct Wi-Fi)
sudo ./install.sh --with-deps --enable --warp-setup # + Cloudflare WARP auto-setup
sudo ./install.sh --with-deps --enable --gen-keys   # + custom WireGuard keypair
```

> Full guide on flags, installed files, and VPN options: [Installation](#installation)

---

## Network Architecture

```
        Internet (WAN)
             |
             v
+--------------------------------------------------------+
|                   LINUX HOST MACHINE                   |
|                                                        |
|   wlan0 --------- Client Uplink (Physical Wi-Fi Card)  |
|     |                                                  |
|     v                                                  |
|   WireGuard Tunnel (wg0 / vpn)                         |
|     |  +- Policy Routing  (ip rule iif ap0 -> table 51820)
|     |  +- Forced DNS      (DNAT client DNS -> 1.1.1.1:53)
|     |  +- TCP MSS Clamp   (--clamp-mss-to-pmtu)        |
|     |  +- Kill-Switch     (DROP unencrypted fallback)  |
|     v                                                  |
|   NAT + Host Isolation (iptables INPUT/FORWARD chains) |
|   IPv6 Leak Protection (ip6tables DROP)                |
|     |                                                  |
|     v                                                  |
|   ap0 --- Virtual AP  (Random MAC: 3a:21:97:...)       |
+---------------------------+----------------------------+
                            | SSID: routerd (WPA2/WPA3 / hidden)
                            | DHCP: dynamic RFC1918 subnet
                            v
               Connected Devices (phones, laptops, IoT)
```

---

## Requirements

- Linux with a Wi-Fi card supporting **concurrent STA + AP mode**
  (verify: `iw list` -> look for `valid interface combinations: ... AP + station`)
- Go 1.22+ (build only - not needed at runtime)
- System packages (auto-installed with `--with-deps`):

| Package | Purpose | Arch | Debian/Ubuntu | Fedora/RHEL | openSUSE | Alpine |
|---|---|---|---|---|---|---|
| `hostapd` | Access point daemon | yes | yes | yes | yes | yes |
| `dnsmasq` | DHCP + DNS | yes | yes | yes | yes | yes |
| `iw` | Wi-Fi interface control | yes | yes | yes | yes | yes |
| `wireless-regdb` | Regulatory database | yes | yes | yes | yes | yes |
| `wireguard-tools` | `wg` + `wg-quick` | yes | yes | yes | yes | yes |
| `openresolv` | DNS resolver for wg-quick | yes | yes | best-effort | best-effort | yes |
| `resolvconf` | DNS fallback | - | yes | - | - | - |
| `iptables` | Firewall rules | yes | yes | yes | yes | yes |
| `iproute2` | `ip`, `tc` commands | yes | yes | yes | yes | yes |
| `wgcf` | Cloudflare WARP profile generator | yes | via go / fallback | via go / fallback | via go / fallback | via go / fallback |

> **Note:** `openresolv` is handled safely by routerd by sanitizing runtime DNS directives, preventing signature mismatch errors on modern systemd-resolved setups.

---

## Installation

### One-command install

```sh
git clone https://github.com/muadzhdz/routerd
cd routerd
sudo ./install.sh --with-deps --enable
```

This command: installs all dependencies (including `wgcf`) -> builds binary -> installs to `/usr/local/bin/routerd` -> installs systemd unit + NetworkManager rule -> creates `/run/routerd` with tmpfiles.d persistence -> reloads systemd -> starts the service.

### Installer flags

| Flag | Description |
|---|---|
| `--with-deps` | Auto-install all system packages (including `wgcf`) |
| `--update-config` | Overwrite `/etc/routerd.conf` with latest sample |
| `--enable` | Run `systemctl enable --now routerd` after install |
| `--warp-setup` | Auto-register and generate Cloudflare WARP profile to `/etc/routerd/vpn.conf` |
| `--gen-keys` | Generate WireGuard keypair in `/etc/routerd/` and pre-fill `PrivateKey` |

### Common examples

```sh
# Install + deps + start + Cloudflare WARP (recommended full setup)
sudo ./install.sh --with-deps --enable --warp-setup

# Install + deps + start + custom WireGuard keys (own VPS / Mullvad / ProtonVPN)
sudo ./install.sh --with-deps --enable --gen-keys

# Install only (no service start, no deps)
sudo ./install.sh

# Refresh config to latest defaults
sudo ./install.sh --update-config
```

### Installed files

| Path | Description |
|---|---|
| `/usr/local/bin/routerd` | Main daemon binary |
| `/etc/routerd.conf` | Main configuration file |
| `/etc/routerd/vpn.conf` | WireGuard VPN profile |
| `/etc/routerd/privatekey` | WireGuard private key (with `--gen-keys`) |
| `/etc/routerd/publickey` | WireGuard public key (with `--gen-keys`) |
| `/etc/systemd/system/routerd.service` | systemd unit (with CAP_NET_ADMIN, CAP_NET_RAW, CAP_KILL) |
| `/etc/NetworkManager/conf.d/90-routerd.conf` | Keep NM away from `ap0` |
| `/etc/tmpfiles.d/routerd.conf` | Recreate `/run/routerd` on reboot |

---

## VPN Setup

### Option A: Cloudflare WARP (free, automated)

```sh
sudo routerd warp-setup
# or during install:
sudo ./install.sh --with-deps --enable --warp-setup
```

The `warp-setup` command uses `wgcf` to automatically register an account with Cloudflare's API, generate keys, and write the active WireGuard profile to `/etc/routerd/vpn.conf`.

Then activate in `/etc/routerd.conf` (or via Web Dashboard):
```ini
ENABLE_VPN=true
VPN_MODE=wireguard
```

### Option B: Custom WireGuard VPS / Mullvad / ProtonVPN

**Auto key generation:**
```sh
sudo ./install.sh --gen-keys
# -> generates /etc/routerd/privatekey + publickey
# -> auto-fills PrivateKey in /etc/routerd/vpn.conf
# -> prints your public key to add to your VPN server
```

Fill in `/etc/routerd/vpn.conf`:
```ini
[Interface]
PrivateKey = <auto-filled by --gen-keys>
Address    = 10.2.0.2/32
DNS        = 1.1.1.1, 1.0.0.1

[Peer]
PublicKey           = <SERVER_PUBLIC_KEY>
Endpoint            = <SERVER_IP>:51820
AllowedIPs          = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
```

Then in `/etc/routerd.conf`:
```ini
ENABLE_VPN=true
VPN_MODE=wireguard
VPN_DNS=1.1.1.1   # or your provider's DNS
```

### Option C: Existing VPN interface (Tailscale, tun0, etc.)

```ini
ENABLE_VPN=true
VPN_MODE=custom
VPN_INTERFACE=tun0
```

### Option D: DPI bypass (no VPN tunnel)

TCP MSS clamping + TTL normalization without a VPN - bypasses ISP throttling/DPI:
```ini
ENABLE_VPN=true
VPN_MODE=dpibypass
```

---

## CLI Reference

```sh
routerd [options] <command>
```

| Command | Description |
|---|---|
| `start` | Start AP, NAT, VPN (foreground) |
| `stop` | Stop everything cleanly |
| `status` | Live status: SSID, channel, clients, VPN handshake |
| `reload` | Atomic config reload with rollback on failure |
| `logs` | Tail hostapd + dnsmasq logs |
| `dashboard` | Start web dashboard (default :8080) |
| `warp-setup` | Auto-register and generate Cloudflare WARP profile |
| `version` | Print version |

Options: `-c <path>` / `--config <path>` - alternate config file

```sh
sudo systemctl start routerd        # start as service
sudo systemctl enable --now routerd # start + enable on boot
sudo routerd status                 # live status
sudo routerd logs                   # tail logs
sudo routerd dashboard              # web UI at http://<gateway>:8080
sudo systemctl reload routerd       # apply config changes
sudo systemctl stop routerd         # stop cleanly
```

---

## Web Dashboard

Enable in `/etc/routerd.conf`:
```ini
DASHBOARD_ENABLED=true
DASHBOARD_PORT=8080
DASHBOARD_PASSWORD=yourpassword   # empty = no auth
DASHBOARD_BIND=0.0.0.0
```

Start: `sudo routerd dashboard` -> open `http://<AP-gateway-IP>:8080` (or `http://localhost:8080`)

| Page | Features |
|---|---|
| **Overview** | Hero banner, 4 stat cards, Wi-Fi QR Code connect modal, live throughput gauge, station list with 4-bar signal meter and kick action, VPN handshake health warning banner |
| **Bandwidth** | Dual total hero metrics, interactive rolling Chart.js graph with pause/resume, per-client percentage distribution breakdown |
| **Logs** | Mac-style developer terminal console, instant keyword search and highlight, stream filter chips (All/hostapd/dnsmasq), log export |
| **Configuration** | Categorized form editor, visual channel selector, privacy switch toggles, raw configuration editor, sticky save bar |
| **VPN** | Visual packet connection topology diagram with particle animation, tunnel latency, WireGuard editor, setup guides |

Security features:
- In-memory session authentication with `crypto/subtle.ConstantTimeCompare`
- Monotonic session validity checks
- IP-based brute-force protection (lockout after repeated failed attempts)
- Strict Content Security Policy (CSP) and security headers
- Responsive Cyber-Dark Glassmorphism theme with automatic mobile bottom navigation dock

---

## Configuration Reference

```ini
# -- Basic ------------------------------------------------------------------
SSID=routerd            # network name
PASSWORD=               # WPA2 password (8-63 chars) or empty for open network
CHANNEL=auto            # auto (follow uplink) or 1-165
INTERFACE_STA=auto      # wireless client interface
INTERFACE_AP=ap0        # virtual AP interface name
UPLINK=auto             # NAT uplink interface
SUBNET=random           # random RFC1918 /24 or fixed e.g. 192.168.50.0/24
COUNTRY=ID              # ISO 3166-1 country code
MAX_CLIENTS=16
DNS=127.0.0.53

# -- Stealth ----------------------------------------------------------------
RANDOM_MAC=true         # random LAA MAC per session
ISOLATE_HOST=true       # block AP clients from host services
SPOOF_TTL=64            # outgoing TTL (hides tethering), 0=off
TOR_MODE=false          # transparent Tor proxy for TCP + DNS
DISABLE_IPV6=true       # block IPv6 leaks
HIDE_SSID=false         # hidden SSID
LIMIT_RATE_MBPS=0       # bandwidth cap (0=unlimited)

# -- VPN --------------------------------------------------------------------
ENABLE_VPN=false
VPN_MODE=wireguard      # wireguard | warp | custom | dpibypass
VPN_CONFIG=/etc/routerd/vpn.conf
VPN_INTERFACE=wg0
VPN_KILL_SWITCH=true    # drop clients if VPN drops
VPN_DNS=1.1.1.1         # forced DNS inside tunnel
WPA3=false              # WPA3-SAE/WPA2-PSK transition mode

# -- Dashboard --------------------------------------------------------------
DASHBOARD_ENABLED=false
DASHBOARD_PORT=8080
DASHBOARD_BIND=0.0.0.0
DASHBOARD_PASSWORD=
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `hostapd exited during startup` | Driver lacks AP mode | `iw list` -> check `AP` in interface combinations. See `/run/routerd/hostapd.log` |
| `cannot start WireGuard VPN` | Missing keys or resolvconf issue | routerd auto-sanitizes DNS to prevent resolvconf mismatch. Ensure `PrivateKey` is valid |
| `VPN Status: Disabled` | `vpn.conf` has unconfigured keys | Run `sudo routerd warp-setup` or supply valid keys |
| `VPN: No Handshake / No internet` | Peer handshake not established | Check endpoint and keys in `/etc/routerd/vpn.conf`. Use dashboard bypass button or set `ENABLE_VPN=false` |
| `No internet on clients` | rp_filter / routing issue | routerd auto-sets policy routing. Check `sudo routerd status` |
| `DNS leak / blocked sites` | ISP hijacking port 53 | routerd forces DNS via `iptables DNAT`. Disable "Private DNS" on Android |
| `ap0 already exists` | Unclean previous shutdown | routerd auto-cleans stale interfaces on startup. Run `sudo routerd stop` then `sudo routerd start` |
| `dashboard: 401` | Session expired | Re-login at `/login.html` |

---

## Development

```sh
make build      # build binary
make test       # run tests with race detector
make coverage   # coverage report
make vet        # go vet
make fmt        # check formatting
```

**Project structure:**
```
routerd/
|-- main.go       - CLI entry, cmdStart/Stop/Reload/Status/Logs/WarpSetup
|-- config.go     - Config struct, LoadConfig
|-- runner.go     - CommandRunner interface (testability)
|-- util.go       - IP math, MAC generation, channel detection
|-- ap.go         - Virtual AP interface lifecycle
|-- nat.go        - iptables rule install/teardown
|-- services.go   - hostapd/dnsmasq config gen, process manager, watchdog
|-- vpn.go        - WireGuard/WARP bringup, runtime DNS sanitization, DPI bypass
|-- state.go      - PID lock, runtime state, clients.json
|-- clients.go    - Station enumeration, lease parsing
|-- doc.go        - Package documentation
+-- dashboard/    - Embedded dark-mode web dashboard SPA
```

---

## Uninstall

```sh
sudo systemctl stop routerd && sudo systemctl disable routerd
sudo make uninstall
```

---

## License

[MIT](LICENSE)
