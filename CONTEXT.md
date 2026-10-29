# Routerd

High-performance eBPF-powered privacy and stealth network router for Linux workstations and Wi-Fi hotspots.

## Language

**Packet Engine**:
The core subsystem managing eBPF program lifecycles, network hook attachments (XDP and TCX), and kernel telemetry sampling.
_Avoid_: BPF Manager, Driver, Hook Service

**Hook**:
A kernel attachment point (XDP ingress or TCX egress/ingress) through which network frames pass before or after the Linux network stack.
_Avoid_: Filter, Interceptor

**MSS Clamper**:
In-kernel TCX logic inspecting and clamping TCP MSS options in SYN/SYN-ACK frames to 1360 bytes to eliminate PMTU blackholes across encrypted tunnels and links.
_Avoid_: TCP Hack, Window Hack

**TLS Inspector**:
In-kernel TCX egress logic inspecting TLS Client Hello frames to track outbound HTTPS handshake activity.
_Avoid_: TLS Sniffer, Packet Spy

**Stats Snapshot**:
An immutable, typed record of packet counters sampled from kernel eBPF maps at a single point in time.
_Avoid_: Map Dump, Telemetry Data

**DNS Resolver**:
The subsystem listening on UDP 53 across host and gateway interfaces, resolving DNS wire messages via encrypted DoH (RFC 8484) and DoT (RFC 7858) upstreams with RFC 1035 TTL-aware caching, RFC 2308 negative caching, and TID rewriting.
_Avoid_: DNS Proxy, Forwarder

**Wire Codec**:
Logic operating directly on raw RFC 1035 octet streams without external third-party AST libraries.
_Avoid_: Packet Parser, DNS Unmarshaler

**AP Controller**:
The subsystem orchestrating virtual Wi-Fi interface creation (ap0), hostapd 802.11 daemon with AP client isolation, dnsmasq DHCP, IPv6 leak defense, active process supervision, and zero-trust iptables NAT rules with deterministic LIFO rollback guarantees.
_Avoid_: Hotspot Manager, Wi-Fi Script

**Telemetry Collector**:
The subsystem sampling kernel virtual filesystems (/proc/net/dev) and eBPF maps at regular intervals, computing bandwidth rates, maintaining history ring buffers, and emitting atomic Snapshot records.
_Avoid_: Metrics Daemon, Stats Poller

**Bandwidth Snapshot**:
An immutable record capturing instantaneous byte counters, rates (bytes/sec), and peak values for a network interface.
_Avoid_: Network Stats, Traffic Info
