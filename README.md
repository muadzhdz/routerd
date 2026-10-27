# routerd

eBPF Stealth Router, In-Memory DNS Sinkhole, and Wi-Fi Access Point Daemon for Linux.

`routerd` turns a Linux machine (Arch Linux, Omarchy, Debian, Ubuntu) into a high-performance, stealth wireless gateway. It couples in-kernel packet manipulation using modern eBPF TCX hooks, a sub-millisecond RFC 1035 DNS wire sinkhole, an isolated Wi-Fi access point with LIFO rollback guarantees, and an optional WireGuard policy-routed VPN uplink.

A detached Unix Domain Socket IPC architecture allows the daemon to run unprivileged under systemd while any terminal user can attach a rich Bubbletea TUI dashboard on demand without root privileges.

---

## Architecture

```text
               +-------------------------------------------------------------+
               |                       routerd Daemon                        |
               |                                                             |
               |  +--------------------+             +--------------------+  |
               |  |  eBPF Engine (TCX) |             |  DoH DNS Resolver  |  |
               |  |  - Ingress Clamp   |             |  - RFC 1035 Wire   |  |
               |  |  - Egress DPI      |             |  - In-Memory Cache |  |
               |  |  - Port Filter     |             |  - Ad Sinkholing   |  |
               |  +---------+----------+             +---------+----------+  |
               +------------|----------------------------------|-------------+
                            |                                  |
   [ Wi-Fi Clients ]        |                                  |
           |                v                                  v
           +=======> [ ap0: 10.42.0.1 ] ──► [ DNAT :53 ] ──► [ 127.0.0.1:53 ]
                            |                                  | (DoH Fallback)
                            | (Forward & MASQ)                 v
                            +──► Table 51820 ──────► [ wg0 / VPN Uplink ]
                            |                        (or Direct WAN Fallback)
                            v
                     [ Internet / Uplink ]
```

### Core Subsystems

- **eBPF Kernel Engine (`pkg/engine`)**: Employs modern Linux 6.6+ TCX (TC BPFv2) hooks for bi-directional packet processing. Intercepts ingress SYN/ACK responses to apply RFC 1624 incremental 16-bit checksum updates and TCP window clamping. Inspects egress HTTPS handshakes for TLS ClientHello payloads using `bpf_skb_pull_data`.
- **In-Memory DNS Sinkhole (`pkg/dns`)**: Intercepts port 53 UDP traffic redirected via Netfilter PREROUTING. Matches requested domains against an in-memory normalized suffix map ($O(1)$ amortized lookups). Blocked advertising and tracker domains receive instantaneous RFC 1035 wire sinkholes (`0.0.0.0` / `::`) with authoritative NOERROR flags. Unblocked queries are resolved over encrypted DNS-over-HTTPS (RFC 8484) with TTL caching and fail-fast SERVFAIL handling.
- **Wi-Fi AP & Stealth NAT (`pkg/hotspot`)**: Creates a virtual `ap0` interface via `nl80211` on top of the physical wireless device, isolated from NetworkManager. Manages `hostapd` and `dnsmasq` lifetimes, configures `route_localnet=1` and iptables NAT rules, and guarantees clean teardown in strict Last-In, First-Out (LIFO) order.
- **WireGuard VPN Uplink (`pkg/vpn`)**: Connects all hotspot traffic through an encrypted WireGuard tunnel (`vpn.conf`). Disables strict reverse path filtering (`rp_filter=0`) to allow asymmetric packet flow, configures policy routing table `51820`, and clamps TCP MSS to PMTU (`--clamp-mss-to-pmtu`) to eliminate fragmentation and blackholes.
- **IPC Daemon & TUI Dashboard (`pkg/ipc`, `pkg/dashboard`)**: Multiplexes telemetry over a non-blocking streaming Unix Domain Socket (`/run/routerd/routerd.sock`). The Bubbletea TUI dashboard attaches and detaches dynamically without interrupting background routing.

---

## Requirements

- **OS**: Linux (Kernel 6.6+ recommended for native TCX, x86_64 / arm64)
- **Runtime Dependencies**:
  - `iproute2` (`ip`)
  - `iptables`
  - `hostapd`
  - `dnsmasq`
  - `iw`
  - `wireguard-tools` (`wg-quick`, optional for VPN uplink)

Check dependencies automatically on your machine:
```bash
make check-deps
```

---

## Installation

### 1. Build from Source

Requires Go 1.22+:

```bash
git clone https://github.com/muadzhdz/routerd.git
cd routerd
make build
```

This compiles an optimized, statically stripped binary (`routerd`, ~9.2 MB).

### 2. System Installation

Install binary, systemd service unit, blocklist, and configuration templates:

```bash
sudo make install
```

Installed paths:
- Binary: `/usr/local/bin/routerd`
- Service: `/etc/systemd/system/routerd.service`
- Configuration: `/etc/routerd/routerd.conf`
- Ad Blocklist: `/etc/routerd/blocklist.txt`
- VPN Profile Template: `/etc/routerd/vpn.conf.example`

### 3. Configuration

Edit `/etc/routerd/routerd.conf`:

```ini
WAN_IFACE=auto
HOTSPOT=true
SSID=routerd
PASSWORD=kopi-hitam123
UPSTREAMS=1.1.1.1, 8.8.8.8
BLOCK_ADS=true
BLOCKLIST_FILE=/etc/routerd/blocklist.txt
VPN_ENABLED=false
VPN_CONFIG=/etc/routerd/vpn.conf
```

### 4. Running the Service

Start and enable the background daemon:

```bash
sudo systemctl enable --now routerd
```

Attach to the live dashboard at any time:

```bash
routerd
```

---

## Command-Line Usage

```text
Usage:
  routerd [command]
  routerd [options]

Commands:
  status               Show status of the running background daemon
  clients              List active connected Wi-Fi clients
  ping                 Probe daemon IPC responsiveness
  version              Print routerd build and version information

Options:
  -c, --config PATH    Configuration file path (default: /etc/routerd/routerd.conf)
  -i, --iface NAME     Target WAN interface (default: auto-detect default route)
  -H, --hotspot        Enable Wi-Fi Access Point & Stealth NAT Router
  -s, --ssid NAME      Wi-Fi Hotspot SSID name (default: from config or 'routerd')
  -p, --password PASS  Wi-Fi Hotspot password (min 8 chars, default: from config)
  -V, --vpn            Enable WireGuard VPN uplink & policy routing
  -d, --headless       Run as background daemon without TUI Dashboard
  -a, --attach         Attach TUI to running background daemon
  -v, --version        Print routerd version and exit
  -h, --help           Show help message
```

---

## Architectural Decision Records (ADRs)

Key architectural decisions are documented in [`docs/adr/`](docs/adr/):

- [ADR-0001](docs/adr/0001-deep-ebpf-packet-engine.md): Deep eBPF Packet Engine & TCX
- [ADR-0002](docs/adr/0002-deep-dns-resolver-engine.md): Deep DNS Resolver Engine & Upstream Failover
- [ADR-0003](docs/adr/0003-stateful-ap-controller-and-lifo-rollback.md): Stateful AP Controller & LIFO Rollback
- [ADR-0004](docs/adr/0004-decouple-telemetry-sampling-from-tui.md): Decouple Telemetry Sampling from TUI
- [ADR-0005](docs/adr/0005-unix-domain-socket-ipc-and-config-loader.md): Unix Domain Socket IPC & Configuration Loader
- [ADR-0006](docs/adr/0006-in-memory-dns-ad-and-malware-sinkhole.md): In-Memory DNS Ad and Malware Sinkholing
- [ADR-0007](docs/adr/0007-wireguard-vpn-uplink-and-policy-routing.md): WireGuard VPN Uplink and Policy Routing

---

## Legacy Version

Looking for the initial web-dashboard prototype? It has been archived in the [`legacy/v0-web`](https://github.com/muadzhdz/routerd/tree/legacy/v0-web) branch.

---

## License

This project is licensed under the [MIT License](LICENSE).
