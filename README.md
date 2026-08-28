<div align="center">

<h1 align="center">routerd</h1>

<p align="center">Turn any Linux machine with a Wi-Fi card into a <b>Stealth Wi-Fi Access Point + Router + Transparent WireGuard VPN Gateway</b> with a single command.</p>

</div>

```sh
sudo systemctl start routerd
```

Nearby devices instantly see a network named `routerd` (configurable) and, once connected, get internet access shared from your machine's existing Wi-Fi connection. No extra hardware needed — it runs on the **same** Wi-Fi card that is already connected to your network.

All connected devices automatically have their traffic routed through a secure **WireGuard / Cloudflare WARP VPN Tunnel** with zero client software installation required on connected phones, laptops, or IoT devices.

- **Written in Go**: Single static binary, zero runtime language dependencies.
- **Unified Daemon**: Access point (`hostapd`), DHCP/DNS (`dnsmasq`), NAT (`iptables`), and VPN (`wg-quick`) managed together with clean lifecycle management.
- **Transparent VPN Gateway**:
  - **WireGuard / WARP VPN Integration**: Route all client traffic through a WireGuard or Cloudflare WARP tunnel (`ENABLE_VPN=true`).
  - **Automated VPN Kill-Switch**: Block unencrypted client fallback traffic if the VPN connection drops (`VPN_KILL_SWITCH=true`).
  - **TCP MSS Clamping**: Automatic TCP segment clamping (`TCPMSS --clamp-mss-to-pmtu`) to prevent packet fragmentation over VPN tunnels.
  - **Forced Encrypted VPN DNS Tunneling**: Force all client DNS requests to `1.1.1.1` inside the WireGuard tunnel to bypass ISP DNS hijacking & censorship.
  - **Policy Routing & Reverse Path Filtering**: Custom `ip rule` and `rp_filter=0` integration to prevent kernel drops under asymmetrical routing.
- **Built-in Stealth & Anonymity Engine**:
  - **Cryptographic Random MAC Address**: Dynamic LAA MAC randomization per session (`RANDOM_MAC=true`) to hide hardware BSSID.
  - **Dynamic RFC1918 Subnets**: Dynamic random subnet selection (`SUBNET=random`) on startup to frustrate tracking.
  - **Host & Port Isolation**: Blocks AP clients from scanning or accessing host services and ports (`ISOLATE_HOST=true`).
  - **TTL Spoofing**: Enforces outgoing TTL (`SPOOF_TTL=64`) to hide tethering and hop counts from your ISP.
  - **Anti-IPv6 Leak Protection**: Disables IPv6 on AP and enforces `ip6tables` DROP rules (`DISABLE_IPV6=true`).
  - **Hidden SSID Broadcast**: Supports hidden access point operation (`HIDE_SSID=true`).
  - **Bandwidth Rate Limiting**: Limit client bandwidth via Linux `tc` qdisc tbf (`LIMIT_RATE_MBPS`).
  - **Tor Transparent Proxying**: Optional transparent proxying of TCP & DNS traffic through Tor (`TOR_MODE=true`).

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
│   WireGuard Tunnel (dev vpn / wg0)                      │
│     │  ├─ Policy Routing (ip rule iif ap0 table 51820) │
│     │  ├─ Forced Encrypted DNS (DNAT -> 1.1.1.1:53)   │
│     │  ├─ TCP MSS Clamping (--clamp-mss-to-pmtu)      │
│     │  └─ Automated Kill-Switch (DROP unencrypted)     │
│     ▼                                                  │
│   NAT + Host Isolation Firewall (INPUT DROP)           │
│   Anti-IPv6 Leak Rules (ip6tables DROP)                │
│     │                                                  │
│     ▼                                                  │
│   ap0 ────────── Virtual AP (Random MAC: 3a:21:97:..)  │
└───────────────────────────┬────────────────────────────┘
                            │ SSID: routerd (WPA2 / Hidden)
                            │ DHCP: Dynamic Subnet (10.x / 172.x / 192.168.x)
                            ▼
┌────────────────────────────────────────────────────────┐
│                    CLIENT DEVICES                      │
│        Smartphones / Laptops / Smart TVs / IoT         │
└────────────────────────────────────────────────────────┘
```

The `mac80211` driver framework allows a single Wi-Fi card to operate simultaneously as a **station (client)** and an **access point (AP)** on the same radio channel. `routerd` automatically detects your active connection's channel (`CHANNEL=auto`) and provisions the AP interface dynamically.

---

## Requirements

- Linux with a Wi-Fi card supporting concurrent STA + AP mode (verify with `iw list` → `valid interface combinations`).
- Packages:
  - **Arch Linux / Manjaro**:
    ```sh
    sudo pacman -S hostapd dnsmasq iw wireless-regdb wireguard-tools openresolv wgcf
    ```
  - **Debian / Ubuntu / Mint**:
    ```sh
    sudo apt install hostapd dnsmasq iw wireless-regdb wireguard-tools openresolv resolvconf
    ```
  - **Fedora / RHEL / Rocky / Alma**:
    ```sh
    sudo dnf install hostapd dnsmasq iw wireless-regdb wireguard-tools
    ```
  - **openSUSE**:
    ```sh
    sudo zypper install hostapd dnsmasq iw wireless-regdb wireguard-tools
    ```

---

## Installation

Run the automated installer script:

```sh
git clone https://github.com/muadzhdz/routerd
cd routerd
make build                        # builds the binary
sudo ./install.sh --with-deps     # installs binary, config, systemd unit, NM rules & dependencies
```

Additional installer flags:
- `--with-deps`: Automatically installs all system package dependencies via `pacman` or `apt-get`.
- `--update-config`: Overwrites `/etc/routerd.conf` with the latest sample configuration.

Installed components:
- `/usr/local/bin/routerd` — main daemon executable
- `/etc/routerd.conf` — main configuration file
- `/etc/systemd/system/routerd.service` — systemd service unit
- `/etc/NetworkManager/conf.d/90-routerd.conf` — NetworkManager unmanaged interface rule

---

## CLI Command Reference

`routerd` provides an intuitive CLI interface for management:

```sh
routerd [options] <command>
```

| Command | Description |
| --- | --- |
| `start` | Starts the access point, NAT firewall, and WireGuard VPN tunnel in foreground. |
| `stop` | Stops the access point, removes NAT rules, tears down VPN tunnel, and cleans up interfaces. |
| `status` | Displays live status: SSID, Channel, Subnet, connected clients, and active VPN mode. |
| `reload` | Regenerates `hostapd` and `dnsmasq` runtime configs and restarts services without dropping interface. |
| `warp-setup` | Generates an automated Cloudflare WARP WireGuard profile template at `/etc/routerd/vpn.conf`. |
| `version` | Prints the version string. |

Examples:
```sh
sudo systemctl start routerd     # start as systemd background service
sudo routerd status              # check live status & connected clients
sudo routerd warp-setup          # generate Cloudflare WARP profile template
sudo systemctl reload routerd    # reload config changes
sudo systemctl stop routerd      # stop service cleanly
```

---

## Configuration Guide (`/etc/routerd.conf`)

Edit `/etc/routerd.conf` to customize `routerd`:

```ini
# Network name broadcasted to nearby devices
SSID=routerd

# WPA2-PSK password (8-63 characters). Empty = open network without password
PASSWORD=changeme

# Radio channel: "auto" (follow active connection) or 1-165
CHANNEL=auto

# Wireless client interface used as internet uplink ("auto")
INTERFACE_STA=auto

# Virtual AP interface name
INTERFACE_AP=ap0

# Uplink interface for NAT ("auto" or specific interface like wg0)
UPLINK=auto

# Subnet for connected clients: "random" (dynamic RFC1918) or static CIDR (192.168.50.0/24)
SUBNET=random

# ISO 3166-1 country code for regulatory compliance
COUNTRY=ID

# Maximum allowed connected clients
MAX_CLIENTS=16

# Upstream DNS server for clients
DNS=127.0.0.53

# --- Stealth & Anonymity Settings ---
RANDOM_MAC=true         # generate random MAC for AP per session
ISOLATE_HOST=true       # block clients from scanning or accessing host services
SPOOF_TTL=64            # outgoing TTL spoofing to hide tethering (0 to disable)
TOR_MODE=false          # transparently proxy TCP & DNS via Tor
DISABLE_IPV6=true       # disable IPv6 on AP & block IPv6 leaks
HIDE_SSID=false         # hide SSID broadcast
LIMIT_RATE_MBPS=0       # rate limit bandwidth in Mbps (0 = unlimited)

# --- Transparent VPN Gateway Settings ---
ENABLE_VPN=true         # route all connected clients through VPN
VPN_MODE=wireguard      # wireguard, warp, custom, or dpibypass
VPN_CONFIG=/etc/routerd/vpn.conf
VPN_INTERFACE=wg0
VPN_KILL_SWITCH=true    # block client internet access if VPN connection drops
```

---

## WireGuard VPN Setup Guide

`routerd` supports three methods for configuring WireGuard VPN:

### Option A: Cloudflare WARP (Free 1-Click Setup)

To use Cloudflare WARP for free uncensored high-speed internet:

1. Generate a Cloudflare WARP profile automatically using `wgcf`:
   ```sh
   wgcf register
   wgcf generate
   sudo mkdir -p /etc/routerd
   sudo cp wgcf-profile.conf /etc/routerd/vpn.conf
   ```
2. Enable VPN in `/etc/routerd.conf`:
   ```ini
   ENABLE_VPN=true
   VPN_MODE=wireguard
   VPN_CONFIG=/etc/routerd/vpn.conf
   ```
3. Restart `routerd`:
   ```sh
   sudo systemctl restart routerd
   sudo routerd status
   ```

### Option B: Custom WireGuard Server (VPS / Mullvad / ProtonVPN)

If you own a WireGuard VPS or subscription:

1. Generate a WireGuard keypair:
   ```sh
   wg genkey | tee privatekey | wg pubkey > publickey
   ```
2. Create `/etc/routerd/vpn.conf`:
   ```ini
   [Interface]
   PrivateKey = <YOUR_PRIVATE_KEY>
   Address = 10.2.0.2/32

   [Peer]
   PublicKey = <SERVER_PUBLIC_KEY>
   Endpoint = <SERVER_IP>:51820
   AllowedIPs = 0.0.0.0/0
   ```
3. Enable VPN in `/etc/routerd.conf` and restart `routerd`:
   ```sh
   sudo systemctl restart routerd
   ```

---

## Troubleshooting Guide

| Symptom | Likely Cause | Solution |
| --- | --- | --- |
| `hostapd exited during startup` | Driver does not support AP mode. | Run `iw list` and verify `AP` is listed under "Supported interface modes". Check `/run/routerd/hostapd.log`. |
| `cannot start WireGuard VPN` | Missing `openresolv` or invalid keys. | Install `openresolv` via `sudo pacman -S openresolv` or `sudo apt install openresolv`. Verify keys in `/etc/routerd/vpn.conf`. |
| `VPN Status: Disabled` | `vpn.conf` contains unconfigured placeholder keys. | `routerd` automatically falls back to direct uplink if keys are unconfigured. Edit `/etc/routerd/vpn.conf` with active keys. |
| `No Internet Access on connected clients` | Routing table or reverse path filtering issue. | `routerd` automatically configures `ip rule add iif ap0 table 51820` and sets `rp_filter=0`. |
| `DNS Leak / Blocked Sites not opening` | ISP DNS hijacking port 53. | `routerd` automatically forces client DNS to `1.1.1.1:53` inside the WireGuard tunnel via `iptables DNAT`. Disable Private DNS in client settings if enabled. |

---

## License

[MIT](LICENSE)
