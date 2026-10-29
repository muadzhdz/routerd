# routerd

<p align="left">
  <strong>English</strong> • <a href="README.id.md">Bahasa Indonesia</a>
</p>

<p align="left">
  <img src="https://img.shields.io/badge/go-1.22+-00ADD8?style=flat-square&logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/ebpf-tcx%20%7C%20xdp-orange?style=flat-square" alt="eBPF" />
  <img src="https://img.shields.io/badge/kernel-6.6+-22c55e?style=flat-square&logo=linux" alt="Kernel" />
  <img src="https://img.shields.io/badge/license-MIT-blue?style=flat-square" alt="License" />
  <img src="https://img.shields.io/badge/release-v1.1.0-indigo?style=flat-square" alt="Release" />
</p>

**routerd** turns any Linux machine into an eBPF-powered privacy gateway and zero-trust Wi-Fi access point. It combines kernel-level packet manipulation using modern eBPF TCX hooks, an in-memory RFC 1035 DoH/DoT DNS sinkhole, single-radio virtual Wi-Fi sharing with AP client isolation, and policy-routed WireGuard VPN tunnels—all orchestrated in a single, statically stripped binary (~9.2 MB) with a decoupled Bubbletea TUI dashboard.

---

## Highlights

- **In-Kernel eBPF TCX Engine**: Intercepts TCP handshakes directly in kernel space to apply RFC 1624 incremental checksum updates and clamp TCP MSS to 1360 bytes, eliminating PMTU blackholes and packet fragmentation. Includes dynamic BPF map port filtering and egress TLS ClientHello inspection.
- **Zero-Trust Wi-Fi Gateway**: Spawns an isolated virtual interface (`ap0`) on top of your existing physical wireless card without dropping the WAN link. Binds DNS to the gateway address (`10.42.0.1:53`) rather than loopback, enforces AP client isolation (`ap_isolate=1`), blocks IPv6 forwarding leaks, and manages `hostapd`/`dnsmasq` with active process supervision.
- **Multi-Protocol DNS Sinkhole**: Intercepts port 53 UDP traffic without third-party parser dependencies. Blocks ad and tracker domains in-memory ($O(1)$) with instantaneous `0.0.0.0` / `::` responses. Unblocked queries resolve over encrypted DNS-over-HTTPS (RFC 8484) and DNS-over-TLS (RFC 7858, Port 853) with TTL-aware caching, RFC 2308 NXDOMAIN negative caching, and automatic upstream failover (Cloudflare, Google, Quad9).
- **WireGuard Policy Routing**: Routes all hotspot traffic through an encrypted WireGuard tunnel using dedicated policy table `51820`, asymmetric reverse-path handling, and peer handshake verification.
- **Decoupled Terminal TUI**: Runs as an unprivileged background daemon under systemd while any user can attach or detach the rich terminal dashboard on demand over a Unix Domain Socket (`/run/routerd/routerd.sock`).

---

## Architecture

```text
               +-------------------------------------------------------------+
               |                       routerd Daemon                        |
               |                                                             |
               |  +--------------------+             +--------------------+  |
               |  |  eBPF Engine (TCX) |             |  DoH/DoT Resolver  |  |
               |  |  - In-Kernel MSS   |             |  - RFC 1035 Wire   |  |
               |  |  - Dynamic Ports   |             |  - In-Memory Cache |  |
               |  |  - TLS Telemetry   |             |  - Ad Sinkholing   |  |
               |  +---------+----------+             +---------+----------+  |
               +------------|----------------------------------|-------------+
                            |                                  |
   [ Wi-Fi Clients ]        |                                  |
           |                v                                  v
           +=======> [ ap0: 10.42.0.1 ] ──► [ DNAT :53 ] ──► [ 10.42.0.1:53 ]
                            |                                  | (DoH / DoT Fallback)
                            | (Forward & MASQ)                 v
                            +──► Table 51820 ──────► [ wg0 / VPN Uplink ]
                            |                        (or Direct WAN Fallback)
                            v
                     [ Internet / Uplink ]
```

---

## Quickstart

### 1. Build and Install

```bash
git clone https://github.com/muadzhdz/routerd.git
cd routerd
make build
sudo make install
```

### 2. Configure

Edit `/etc/routerd/routerd.conf`:

```ini
WAN_IFACE=auto
HOTSPOT=true
SSID=routerd
PASSWORD=kopi-hitam123
CLIENT_ISOLATION=true
UPSTREAMS=1.1.1.1, 8.8.8.8, 9.9.9.9, tls://1.1.1.1:853, tls://9.9.9.9:853
BLOCK_ADS=true
BLOCKLIST_FILE=/etc/routerd/blocklist.txt
VPN_ENABLED=false
VPN_CONFIG=/etc/routerd/vpn.conf
```

### 3. Run

```bash
# Start the background daemon
sudo systemctl enable --now routerd

# Attach live TUI dashboard at any time
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
  version              Print build and version information

Options:
  -c, --config PATH    Configuration file path (default: /etc/routerd/routerd.conf)
  -i, --iface NAME     Target WAN interface (default: auto-detect default route)
  -H, --hotspot        Enable Wi-Fi Access Point & Zero-Trust NAT Router
  -s, --ssid NAME      Wi-Fi Hotspot SSID name (default: from config or 'routerd')
  -p, --password PASS  Wi-Fi Hotspot password (min 8 chars, default: from config)
  -V, --vpn            Enable WireGuard VPN uplink & policy routing
  -d, --headless       Run as background daemon without TUI Dashboard
  -a, --attach         Attach TUI to running background daemon
  -v, --version        Print routerd version and exit
  -h, --help           Show help message
```

---

## Requirements

- **Linux Kernel**: 6.6+ recommended for native TCX (x86_64 / arm64)
- **Runtime Packages**: `iproute2`, `iptables`, `hostapd`, `dnsmasq`, `iw`, `wireguard-tools` (optional)

Verify dependencies on your machine:
```bash
make check-deps
```

---

## Architectural Decision Records (ADRs)

Design choices and security rationales are documented under [`docs/adr/`](docs/adr/):

- [ADR-0001](docs/adr/0001-deep-ebpf-packet-engine.md): Deep eBPF Packet Engine & TCX Hooks
- [ADR-0002](docs/adr/0002-deep-dns-resolver-engine.md): Multi-Protocol DNS Resolver & DoT/DoH Failover
- [ADR-0003](docs/adr/0003-stateful-ap-controller-and-lifo-rollback.md): Stateful AP Controller, Zero-Trust Isolation & LIFO Rollback
- [ADR-0004](docs/adr/0004-decouple-telemetry-sampling-from-tui.md): Decoupled Telemetry Sampling from TUI
- [ADR-0005](docs/adr/0005-unix-domain-socket-ipc-and-config-loader.md): Unix Domain Socket IPC & Configuration Loader
- [ADR-0006](docs/adr/0006-in-memory-dns-ad-and-malware-sinkhole.md): In-Memory DNS Ad & Malware Sinkholing
- [ADR-0007](docs/adr/0007-wireguard-vpn-uplink-and-policy-routing.md): WireGuard VPN Uplink & Policy Routing

---

## License

This project is licensed under the [MIT License](LICENSE).
