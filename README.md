<div align="center">

<h1 align="center">routerd</h1>

<p align="center">Turn any Linux machine with a Wi-Fi card into a <b>Stealth Wi-Fi access point + router</b> with a single command.</p>

</div>

```sh
sudo systemctl start routerd
```

Nearby devices instantly see a network named `routerd` (configurable) and, once
connected, get internet access shared from your machine's existing Wi-Fi
connection. No extra hardware needed — it runs on the **same** Wi-Fi card that
is already connected to your network.

- **Written in Go**, single static binary, zero dependencies.
- **Access point (hostapd) + DHCP/DNS (dnsmasq) + NAT (iptables)** all managed together with clean start/stop.
- **Built-in Stealth & Anonymity Engine**:
  - **Random MAC Address**: Cryptographic MAC randomization per session (hides hardware BSSID).
  - **Randomized Subnets**: Dynamic RFC1918 subnet selection (`SUBNET=random`) on startup.
  - **Host & Port Isolation**: Blocks AP clients from scanning or accessing host services/ports.
  - **TTL Spoofing**: Enforces outgoing TTL (`SPOOF_TTL=64`) to hide tethering & hop counts from ISP.
  - **Anti-IPv6 Leak Protection**: Automatically disables IPv6 on AP & installs `ip6tables` DROP rules.
  - **Tor & VPN Ready**: Supports transparent Tor proxying (`TOR_MODE=true`) and WireGuard VPN uplinks (`UPLINK=wg0`).
- **WPA2-PSK password support** (or open network) & optional hidden SSID (`HIDE_SSID=true`).
- **Auto-follows channel** of your current Wi-Fi connection.

## How it works

```
        Internet
       (your WiFi)
            │
            ▼
┌──────────────────────────────────────────────────┐
│                    MACHINE                       │
│                                                  │
│    wlan0  ──  client uplink                      │
│       │                                          │
│       ▼                                          │
│    NAT + Forwarding + TTL Spoofing (64)          │
│    Host Isolation Firewall (INPUT DROP)          │
│    Anti-IPv6 Leak Rules                          │
│       │                                          │
│       ▼                                          │
│    ap0  ──  virtual AP (Crypto Random MAC)       │
└───────┬──────────────────────────────────────────┘
        │  SSID: routerd (WPA2 / Hidden)
        │  DHCP: Dynamic Subnet (10.x / 172.x / 192.168.x)
        ▼
┌──────────────────────────────────────────────────┐
│                    CLIENTS                       │
│         phones / laptops / IoT                   │
└──────────────────────────────────────────────────┘
```

The driver (mac80211) lets one card act as a **station (client)** and an
**access point** at the same time. The only constraint is that both must use
the **same radio channel**, so `routerd` picks the AP channel automatically
from your current connection (`CHANNEL=auto`).

## Requirements

- Linux with a Wi-Fi card whose driver supports concurrent STA + AP
  (check with `iw list` → `valid interface combinations`).
- Packages:
  - **Arch Linux**: `sudo pacman -S hostapd dnsmasq iw wireless-regdb`
  - **Debian/Ubuntu**: `sudo apt install hostapd dnsmasq iw wireless-regdb`
    (On Ubuntu, enable the `universe` repository — `hostapd` is in it.)

## Install

```sh
git clone https://github.com/muadzhdz/routerd
cd routerd
make build                     # optional, builds the binary
sudo ./install.sh              # binary + config + systemd unit
sudo ./install.sh --with-deps  # ... plus hostapd/dnsmasq/iw/wireless-regdb
sudo ./install.sh --update-config # ... force update /etc/routerd.conf with latest options
```

This installs:

- `/usr/local/bin/routerd` — the daemon
- `/etc/routerd.conf` — configuration
- `/etc/systemd/system/routerd.service` — systemd unit
- `/etc/NetworkManager/conf.d/90-routerd.conf` — keeps the AP interface unmanaged

## Configure

Edit `/etc/routerd.conf`:

```ini
SSID=routerd            # network name
PASSWORD=changeme       # WPA2 password (8-63 chars), empty = open
CHANNEL=auto            # auto = follow your Wi-Fi's channel
INTERFACE_AP=ap0        # virtual AP interface name
SUBNET=random           # random = dynamic RFC1918 subnet, or static CIDR (e.g. 192.168.50.0/24)
COUNTRY=ID              # your ISO 3166-1 country code
MAX_CLIENTS=16

# Stealth & Security Options
RANDOM_MAC=true         # random MAC per session (hides hardware BSSID)
ISOLATE_HOST=true       # block client access to host ports & services
SPOOF_TTL=64            # hide tethering / hop counts from ISP
TOR_MODE=false          # transparently proxy traffic through Tor
DISABLE_IPV6=true       # disable IPv6 on AP & block IPv6 leaks
HIDE_SSID=false         # hide SSID broadcast (hidden network)
LIMIT_RATE_MBPS=0       # rate limit bandwidth in Mbps (0 = unlimited)
```

## Usage

```sh
sudo systemctl start routerd     # turn on the access point
sudo routerd status              # SSID, channel, connected clients
sudo systemctl reload routerd    # apply SSID/password/channel/subnet changes
sudo systemctl restart routerd   # full restart (recreates the AP interface)
sudo systemctl enable routerd    # start automatically on boot
sudo systemctl stop routerd      # turn it off (cleans everything up)
```

Or run it directly (foreground):

```sh
sudo routerd -c /etc/routerd.conf start
```

## Troubleshooting

| Symptom | Likely cause / fix |
| --- | --- |
| `hostapd exited during startup` | AP mode not supported by the driver. Check `iw list` for `AP` under "Supported interface modes". See `/run/routerd/hostapd.log`. |
| `no associated wireless client interface found` | The machine is not connected to any Wi-Fi. Connect first, or set `INTERFACE_STA`. |
| AP does not appear / no internet | `CHANNEL=auto` picks your Wi-Fi's channel; make sure you are connected. On 5 GHz avoid DFS channels (52–64, 100–140). |
| 5 GHz does not work | Set a valid `COUNTRY` and install `wireless-regdb`. |
| Windows refuses to connect | We use WPA2 (`wpa=2`), which is the most compatible. |
| Slow speeds | One radio is shared between client and AP; that's normal for this setup. |

## Limitations

- The AP shares the radio (and channel) with your Wi-Fi connection.
- Only 2 interfaces total (1 STA + 1 AP) — this is a driver limit.
- Requires root; designed primarily for systemd-based distros.

## License

[MIT](LICENSE)
