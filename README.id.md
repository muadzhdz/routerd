# routerd

<p align="left">
  <a href="README.md">English</a> • <strong>Bahasa Indonesia</strong>
</p>

<p align="left">
  <img src="https://img.shields.io/badge/go-1.22+-00ADD8?style=flat-square&logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/ebpf-tcx%20%7C%20xdp-orange?style=flat-square" alt="eBPF" />
  <img src="https://img.shields.io/badge/kernel-6.6+-22c55e?style=flat-square&logo=linux" alt="Kernel" />
  <img src="https://img.shields.io/badge/license-MIT-blue?style=flat-square" alt="License" />
  <img src="https://img.shields.io/badge/release-v1.1.0-indigo?style=flat-square" alt="Release" />
</p>

**routerd** mengubah komputer Linux Anda menjadi gateway privasi berkinerja tinggi dan titik akses (Wi-Fi Access Point) berstandar *Zero-Trust*. Menggabungkan manipulasi paket tingkat kernel menggunakan hook eBPF TCX modern, sinkhole DNS DoH/DoT berbasis RFC 1035 di memori, pembagian virtual radio Wi-Fi tunggal dengan isolasi klien, serta perutean terisolasi WireGuard VPN—seluruhnya dikemas dalam satu biner statis mandiri (~9.2 MB) dengan dashboard terminal Bubbletea TUI.

---

## Keunggulan Utama

- **Engine eBPF TCX Tingkat Kernel**: Memproses jabat tangan (handshake) TCP langsung di ruang kernel untuk menerapkan pembaruan checksum inkremental RFC 1624 dan melakukan *MSS clamping* ke 1360 byte. Mencegah *PMTU blackholes* dan fragmentasi paket tanpa membebani CPU. Dilengkapi pemfilteran port dinamis berbasis eBPF Hash Map dan inspeksi egress TLS ClientHello.
- **Gateway Wi-Fi Zero-Trust**: Membuat antarmuka virtual terisolasi (`ap0`) di atas perangkat Wi-Fi fisik tanpa memutus koneksi internet utama (WAN). Mengarahkan DNS langsung ke IP gateway (`10.42.0.1:53`) tanpa membuka celah loopback host, menerapkan isolasi klien (`ap_isolate=1`) agar sesama perangkat tidak dapat saling intip/serang, memblokir kebocoran trafik IPv6, dan memantau proses `hostapd`/`dnsmasq` secara aktif.
- **Sinkhole DNS Multi-Protokol**: Mencegat kueri UDP port 53 secara sub-milidetik tanpa pustaka pihak ketiga. Domain iklan dan pelacak langsung dijawab di memori ($O(1)$) dengan respon sinkhole `0.0.0.0` / `::`. Domain yang diizinkan diselesaikan melalui DNS-over-HTTPS (RFC 8484) dan DNS-over-TLS (RFC 7858, Port 853) dengan cache TTL, *negative caching* NXDOMAIN (RFC 2308), dan failover otomatis ke Cloudflare, Google, atau Quad9.
- **WireGuard Policy Routing**: Mengarahkan seluruh trafik hotspot melewati terowongan VPN terenkripsi menggunakan tabel *policy routing* khusus `51820`, penanganan *asymmetric reverse-path* (`rp_filter=0`), dan verifikasi berkala jabat tangan peer.
- **Dashboard TUI Terminal yang Terpisah (Decoupled)**: Daemon berjalan di latar belakang sebagai layanan systemd, sementara pengguna terminal biasa dapat membuka atau menutup dashboard TUI secara bebas kapan saja melalui Unix Domain Socket (`/run/routerd/routerd.sock`) tanpa mengganggu proses perutean.

---

## Arsitektur

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

## Panduan Cepat

### 1. Kompilasi dan Instalasi

```bash
git clone https://github.com/muadzhdz/routerd.git
cd routerd
make build
sudo make install
```

### 2. Konfigurasi

Sesuaikan file `/etc/routerd/routerd.conf`:

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

### 3. Menjalankan Layanan

```bash
# Aktifkan dan jalankan daemon background via systemd
sudo systemctl enable --now routerd

# Buka dashboard interaktif TUI kapan saja
routerd
```

---

## Perintah Command-Line

```text
Penggunaan:
  routerd [perintah]
  routerd [opsi]

Perintah:
  status               Melihat status daemon yang sedang berjalan
  clients              Daftar stasiun Wi-Fi yang sedang terhubung
  ping                 Uji responsivitas soket IPC daemon
  version              Cetak versi dan commit informasi rilis

Opsi:
  -c, --config PATH    Lokasi file konfigurasi (default: /etc/routerd/routerd.conf)
  -i, --iface NAME     Antarmuka jaringan WAN (default: deteksi otomatis rute default)
  -H, --hotspot        Aktifkan Wi-Fi Access Point & Router NAT Zero-Trust
  -s, --ssid NAME      Nama SSID Wi-Fi Hotspot (default: dari konfigurasi atau 'routerd')
  -p, --password PASS  Kata sandi Wi-Fi Hotspot (min 8 karakter)
  -V, --vpn            Aktifkan uplink VPN WireGuard & policy routing
  -d, --headless       Jalankan sebagai daemon murni tanpa TUI Dashboard
  -a, --attach         Sambungkan TUI Dashboard ke daemon yang sedang aktif
  -v, --version        Cetak versi routerd lalu keluar
  -h, --help           Tampilkan bantuan penggunaan
```

---

## Kebutuhan Sistem

- **Sistem Operasi**: Linux (Direkomendasikan Kernel 6.6+ untuk native TCX, x86_64 / arm64)
- **Ketergantungan Paket**: `iproute2`, `iptables`, `hostapd`, `dnsmasq`, `iw`, `wireguard-tools` (opsional untuk VPN)

Periksa ketergantungan paket di sistem Anda:
```bash
make check-deps
```

---

## Catatan Keputusan Arsitektur (ADRs)

Seluruh keputusan teknis dan justifikasi keamanan dicatat secara terstruktur di [`docs/adr/`](docs/adr/):

- [ADR-0001](docs/adr/0001-deep-ebpf-packet-engine.md): Deep eBPF Packet Engine & Hook TCX
- [ADR-0002](docs/adr/0002-deep-dns-resolver-engine.md): DNS Resolver Multi-Protokol & Failover DoT/DoH
- [ADR-0003](docs/adr/0003-stateful-ap-controller-and-lifo-rollback.md): Pengontrol AP Stateful, Isolasi Zero-Trust & LIFO Rollback
- [ADR-0004](docs/adr/0004-decouple-telemetry-sampling-from-tui.md): Pemisahan Telemetri Kernel dari Antarmuka TUI
- [ADR-0005](docs/adr/0005-unix-domain-socket-ipc-and-config-loader.md): IPC Unix Domain Socket & Loader Konfigurasi
- [ADR-0006](docs/adr/0006-in-memory-dns-ad-and-malware-sinkhole.md): Sinkhole Iklan & Malware DNS di Memori
- [ADR-0007](docs/adr/0007-wireguard-vpn-uplink-and-policy-routing.md): WireGuard VPN Uplink & Policy Routing

---

## Lisensi

Proyek ini dirilis di bawah naungan [MIT License](LICENSE).
