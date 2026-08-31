<div align="center">

<h1 align="center">routerd</h1>

<p align="center">Turn any Linux machine with a Wi-Fi card into a <b>Stealth Wi-Fi Access Point + Router + Transparent WireGuard VPN Gateway</b> — in a single command.</p>

</div>

```sh
git clone https://github.com/muadzhdz/routerd
cd routerd
sudo ./install.sh --with-deps --enable
```

Nearby devices instantly see a network named `routerd` (configurable) and, once connected, get internet access shared from your machine's existing Wi-Fi connection. **No extra hardware needed** — it runs on the same Wi-Fi card that is already connected to your network.

All connected client traffic is transparently routed through a **WireGuard / Cloudflare WARP VPN tunnel** with zero configuration required on connected phones, laptops, or IoT devices.

- **Written in Go** — single static binary, zero runtime dependencies
- **Unified daemon** — `hostapd`, `dnsmasq`, `iptables`, and `wg-quick` managed together with clean lifecycle, watchdog, and atomic reload
- **Transparent VPN gateway** — WireGuard / WARP / custom VPN / DPI bypass
- **Built-in stealth engine** — random MAC, random subnet, TTL spoofing, IPv6 leak protection, host isolation
- **Web dashboard** — dark-mode SPA with live status, bandwidth graphs, log tail, config editor, VPN management
- **159 unit tests, race-detector clean, 44.7% coverage**

---

## Network Architecture

```
        Internet (WAN)
             │
             ▼
┌────────────────────────────────────────────────────────┐
│                   LINUX HOST MACHINE                   │
│                                                        │
│   wlan0 ──────── Client Uplink (Physical Wi-Fi Card)  │
│     │                                                  │
│     ▼                                                  │
│   WireGuard Tunnel (wg0 / vpn)                         │
│     │  ├─ Policy Routing  (ip rule iif ap0 → table 51820)
│     │  ├─ Forced DNS      (DNAT client DNS → 1.1.1.1:53)
│     │  ├─ TCP MSS Clamp   (--clamp-mss-to-pmtu)       │
│     │  └─ Kill-Switch     (DROP unencrypted fallback)  │
│     ▼                                                  │
│   NAT + Host Isolation (iptables INPUT/FORWARD chains) │
│   IPv6 Leak Protection (ip6tables DROP)                │
│     │                                                  │
│     ▼                                                  │
│   ap0 ─── Virtual AP  (Random MAC: 3a:21:97:…)        │
└───────────────────────────┬────────────────────────────┘
                            │ SSID: routerd (WPA2/WPA3 / hidden)
                            │ DHCP: dynamic RFC1918 subnet
                            ▼
               Connected Devices (phones, laptops, IoT)
```

---

## Requirements

- Linux with a Wi-Fi card that supports **concurrent STA + AP mode** (verify with `iw list` — look for `valid interface combinations: … AP + station`)
- Go 1.22+ (only needed to build from source — not needed at runtime)
- System packages (installed automatically with `--with-deps`):

| Package | Purpose | Arch | Debian/Ubuntu | Fedora/RHEL | openSUSE | Alpine |
|---|---|---|---|---|---|---|
| `hostapd` | Access point daemon | ✅ | ✅ | ✅ | ✅ | ✅ |
| `dnsmasq` | DHCP + DNS | ✅ | ✅ | ✅ | ✅ | ✅ |
| `iw` | Wi-Fi interface control | ✅ | ✅ | ✅ | ✅ | ✅ |
| `wireless-regdb` | Regulatory database | ✅ | ✅ | ✅ | ✅ | ✅ |
| `wireguard-tools` | `wg` + `wg-quick` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `openresolv` | DNS resolver for wg-quick | ✅ | ✅ | best-effort | best-effort | ✅ |
| `resolvconf` | DNS resolver fallback | — | ✅ | — | — | — |
| `iptables` | Firewall rules | ✅ | ✅ | ✅ | ✅ | ✅ |
| `iproute2` | `ip`, `tc` commands | ✅ | ✅ | ✅ | ✅ | ✅ |

> **Note:** `openresolv` is required by `wg-quick` to configure DNS inside the VPN tunnel. On Fedora/RHEL/openSUSE it may not be in default repos — `routerd` will warn and fall back gracefully, but DNS may leak outside the tunnel without it.

---

## Installation

### One-command install (recommended)

```sh
git clone https://github.com/muadzhdz/routerd
cd routerd
sudo ./install.sh --with-deps --enable
```

This single command:
1. Detects your OS and installs all system dependencies
2. Builds the `routerd` binary with Go
3. Installs binary to `/usr/local/bin/routerd`
4. Installs systemd unit, NetworkManager rule, sample config
5. Creates `/run/routerd` runtime dir + tmpfiles.d persistence rule
6. Reloads systemd and starts the service

### Installer flags

| Flag | Description |
|---|---|
| `--with-deps` | Auto-install all system packages (hostapd, dnsmasq, wireguard-tools, etc.) |
| `--update-config` | Overwrite `/etc/routerd.conf` with the latest sample config |
| `--enable` | Run `systemctl enable --now routerd` after install (start on boot) |
| `--warp-setup` | Auto-generate a Cloudflare WARP WireGuard profile to `/etc/routerd/vpn.conf` |
| `--gen-keys` | Generate a WireGuard keypair in `/etc/routerd/` and pre-fill `PrivateKey` into `vpn.conf` |

### Examples

```sh
# Install + deps + auto-start + Cloudflare WARP VPN (easiest)
sudo ./install.sh --with-deps --enable --warp-setup

# Install + deps + auto-start + custom WireGuard keys (for own VPS / Mullvad / ProtonVPN)
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
| `/etc/systemd/system/routerd.service` | systemd unit |
| `/etc/NetworkManager/conf.d/90-routerd.conf` | Keep NM away from `ap0` |
| `/etc/tmpfiles.d/routerd.conf` | Recreate `/run/routerd` on reboot |

---

## VPN Setup

### Option A: Cloudflare WARP (free, automated)

```sh
# If already installed:
sudo routerd warp-setup

# Or during install:
sudo ./install.sh --with-deps --enable --warp-setup
```

Then enable VPN in `/etc/routerd.conf`:
```ini
ENABLE_VPN=true
VPN_MODE=wireguard
```

Restart: `sudo systemctl restart routerd`

### Option B: Custom WireGuard VPS / Mullvad / ProtonVPN

**Auto key generation:**
```sh
sudo ./install.sh --gen-keys
# → generates /etc/routerd/privatekey and /etc/routerd/publickey
# → auto-fills PrivateKey in /etc/routerd/vpn.conf
# → prints your public key to share with your VPN server
```

**Or manual:**
```sh
wg genkey | sudo tee /etc/routerd/privatekey | wg pubkey | sudo tee /etc/routerd/publickey
```

Then fill in `/etc/routerd/vpn.conf`:
```ini
[Interface]
PrivateKey = <YOUR_PRIVATE_KEY>
Address    = 10.2.0.2/32
DNS        = 1.1.1.1, 1.0.0.1

[Peer]
PublicKey         = <SERVER_PUBLIC_KEY>
Endpoint          = <SERVER_IP>:51820
AllowedIPs        = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
```

Enable in `/etc/routerd.conf`:
```ini
ENABLE_VPN=true
VPN_MODE=wireguard
VPN_DNS=1.1.1.1   # or your provider's DNS (e.g. 10.64.0.1 for Mullvad)
```

### Option C: Use an existing VPN interface

```ini
ENABLE_VPN=true
VPN_MODE=custom
VPN_INTERFACE=tun0   # or tailscale0, wg0, etc.
```

### Option D: DPI bypass (no VPN)

Applies TCP MSS clamping + TTL normalization without a VPN tunnel — useful for bypassing ISP DPI/throttling:
```ini
ENABLE_VPN=true
VPN_MODE=dpibypass
```

---

## CLI Command Reference

```sh
routerd [options] <command>
```

| Command | Description |
|---|---|
| `start` | Start AP, NAT routing, and VPN tunnel (foreground) |
| `stop` | Stop everything: AP, DHCP, NAT rules, VPN tunnel |
| `status` | Live status: SSID, channel, subnet, clients, VPN |
| `reload` | Atomic reload: re-apply all config changes (VPN, NAT, DNS, isolation) with rollback on failure |
| `logs` | Tail hostapd + dnsmasq logs simultaneously |
| `dashboard` | Start web dashboard (default port 8080) |
| `warp-setup` | Auto-generate Cloudflare WARP profile to `/etc/routerd/vpn.conf` |
| `version` | Print version |

Options: `-c <path>` / `--config <path>` — use alternate config file

```sh
sudo systemctl start routerd        # start as background service
sudo routerd status                 # live status + connected clients
sudo routerd logs                   # tail logs
sudo routerd dashboard              # web UI at http://<gateway>:8080
sudo routerd warp-setup             # generate WARP config
sudo systemctl reload routerd       # apply config changes
sudo systemctl stop routerd         # stop cleanly
sudo systemctl enable --now routerd # start now + on every boot
```

---

## Web Dashboard

Enable in `/etc/routerd.conf`:
```ini
DASHBOARD_ENABLED=true
DASHBOARD_PORT=8080
DASHBOARD_PASSWORD=yourpassword   # leave empty for no auth
DASHBOARD_BIND=0.0.0.0            # or 127.0.0.1 for localhost-only
```

Start: `sudo routerd dashboard`
Open: `http://<AP-gateway-IP>:8080`

| Page | Features |
|---|---|
| **Overview** | Live AP status, connected clients (MAC/IP/hostname), real-time bandwidth via WebSocket |
| **Bandwidth** | 60-second rolling Chart.js graph, per-client TX/RX breakdown |
| **Logs** | Live tail of hostapd + dnsmasq with filter, pause/resume, auto-scroll |
| **Configuration** | Form editor + raw textarea for `/etc/routerd.conf`, Save & Reload |
| **VPN** | WireGuard status (endpoint, latency, last handshake), config editor, setup guide |

**Security:**
- Session cookie auth (`HttpOnly`, `SameSite=Lax`, `Secure` on HTTPS, 24h expiry)
- Brute force protection — 5 failed attempts → 5 min lockout per IP
- Same-origin CORS + WebSocket origin validation — no wildcard `Access-Control-Allow-Origin`

---

## Configuration Reference (`/etc/routerd.conf`)

```ini
# ── Basic ──────────────────────────────────────────────────────────────────
SSID=routerd            # network name (1-32 chars, no = or newlines)
PASSWORD=               # WPA2 password (8-63 chars) or empty for open network
CHANNEL=auto            # auto (follow uplink) or 1-165
INTERFACE_STA=auto      # wireless client interface (auto-detected)
INTERFACE_AP=ap0        # virtual AP interface name
UPLINK=auto             # NAT uplink (auto = default route interface)
SUBNET=random           # random RFC1918 /24 or fixed e.g. 192.168.50.0/24
COUNTRY=ID              # ISO 3166-1 country code for radio regulations
MAX_CLIENTS=16          # max connected stations
DNS=127.0.0.53          # upstream DNS (overridden by VPN_DNS when VPN active)

# ── Stealth & Anonymity ────────────────────────────────────────────────────
RANDOM_MAC=true         # random locally-administered MAC per session
ISOLATE_HOST=true       # block AP clients from reaching host services
SPOOF_TTL=64            # set outgoing TTL (hides tethering from ISP), 0=off
TOR_MODE=false          # transparent Tor proxy for TCP + DNS
DISABLE_IPV6=true       # disable IPv6 on AP + block IPv6 leaks (recommended)
HIDE_SSID=false         # hidden SSID broadcast
LIMIT_RATE_MBPS=0       # bandwidth limit per AP (0 = unlimited)

# ── VPN Gateway ────────────────────────────────────────────────────────────
ENABLE_VPN=false        # enable transparent VPN routing for all AP clients
VPN_MODE=wireguard      # wireguard | warp | custom | dpibypass
VPN_CONFIG=/etc/routerd/vpn.conf
VPN_INTERFACE=wg0       # used for custom mode or to name wg-quick interface
VPN_KILL_SWITCH=true    # drop client traffic if VPN drops
VPN_DNS=1.1.1.1         # forced DNS inside VPN tunnel (all client DNS → here)
WPA3=false              # WPA3-SAE/WPA2-PSK transition mode

# ── Dashboard ──────────────────────────────────────────────────────────────
DASHBOARD_ENABLED=false
DASHBOARD_PORT=8080
DASHBOARD_BIND=0.0.0.0
DASHBOARD_PASSWORD=     # empty = no auth (not recommended on public APs)
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `hostapd exited during startup` | Driver lacks AP mode | `iw list` → check `valid interface combinations` includes `AP`. Check `/run/routerd/hostapd.log` |
| `cannot start WireGuard VPN` | Missing `openresolv` or bad keys | Install `openresolv`. Verify `PrivateKey` in `/etc/routerd/vpn.conf` is not commented out |
| `VPN Status: Disabled` | `vpn.conf` has placeholder keys | Run `sudo routerd warp-setup` or fill in real keys. routerd auto-detects and falls back gracefully |
| `No internet on connected clients` | rp_filter / routing issue | routerd auto-sets `ip rule add iif ap0 table 51820` and `rp_filter=0`. Check `sudo routerd status` |
| `DNS leak / blocked sites` | ISP hijacking port 53 | routerd forces DNS via `iptables DNAT → VPN_DNS:53`. Disable "Private DNS" on Android clients |
| `iptables: exit status 4` | System uses `iptables-nft` | routerd auto-detects and strips `-w` flag for nft backends — should be transparent |
| `ap0 already exists on reload` | Previous unclean shutdown | `sudo routerd stop` then `sudo routerd start` to force cleanup |
| `dashboard: 401 Unauthorized` | Session expired or wrong password | Clear cookies or re-login at `http://<gateway>:8080/login.html` |

---

## Uninstall

```sh
sudo systemctl stop routerd
sudo systemctl disable routerd
sudo make uninstall
```

---

## Development

```sh
# Build
make build

# Run tests with race detector
make test

# Generate coverage report
make coverage

# Check formatting
make fmt

# Vet
make vet
```

**Project structure:**
```
routerd/
├── main.go         — CLI entry point, cmdStart/Stop/Reload/Status/Logs
├── config.go       — Config struct, DefaultConfig, LoadConfig
├── runner.go       — CommandRunner interface (testability)
├── util.go         — IP math, MAC generation, channel detection
├── ap.go           — Virtual AP interface lifecycle (iw, ip)
├── nat.go          — iptables rule install/teardown
├── services.go     — hostapd/dnsmasq config gen, process manager, watchdog
├── vpn.go          — WireGuard/WARP bringup, DPI bypass, wgcf integration
├── state.go        — PID lock, runtime state file, clients.json writer
├── clients.go      — Station enumeration, lease parsing
├── doc.go          — Package-level documentation
└── dashboard/      — Embedded dark-mode web dashboard (HTTP + WebSocket)
```

---

## License

[MIT](LICENSE)
