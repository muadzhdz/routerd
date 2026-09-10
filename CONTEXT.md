# Routerd

High-performance eBPF-powered privacy and stealth network router for Linux workstations and Wi-Fi hotspots.

## Language

**Packet Engine**:
The core subsystem managing eBPF program lifecycles, network hook attachments (XDP and TCX), and kernel telemetry sampling.
_Avoid_: BPF Manager, Driver, Hook Service

**Hook**:
A kernel attachment point (XDP ingress or TCX egress/ingress) through which network frames pass before or after the Linux network stack.
_Avoid_: Filter, Interceptor

**Window Clamping**:
Rewriting TCP MSS and window size headers in SYN/SYN-ACK packets to prevent fragmentation and middlebox throttling.
_Avoid_: TCP Hack, MSS Clamper

**DPI Hunter**:
In-kernel TCX egress logic inspecting TLS Client Hello frames to cloak outbound traffic signatures.
_Avoid_: TLS Sniffer, Packet Spy

**Stats Snapshot**:
An immutable, typed record of packet counters sampled from kernel eBPF maps at a single point in time.
_Avoid_: Map Dump, Telemetry Data
