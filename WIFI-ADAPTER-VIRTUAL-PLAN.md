# Rencana Implementasi Virtual Wi-Fi Adapter untuk Pentest routerd

**Dokumen:** Comprehensive Plan untuk membangun Wi-Fi adapter virtual (software-based / simulated)
**Tujuan Utama:** Testing keamanan & ketahanan (`routerd`) secara ujung-ke-ujung **tanpa perangkat keras (adapter fisik) tambahan**
**Status:** IMPLEMENTED ✓

---

## Status Implementasi

| Komponen | File | Status |
|----------|------|--------|
| udev rules (pin interface names) | `vwifi/70-vwifi.rules` | ✅ Done |
| Setup / teardown script | `vwifi/setup-vwifi.sh` | ✅ Done |
| routerd config for testing | `vwifi/routerd-test.conf` | ✅ Done |
| Client connect script | `vwifi/vwifi-client.sh` | ✅ Done |
| WPA2 handshake capture & crack | `pentest/01-handshake.sh` | ✅ Done |
| Deauth attack test | `pentest/02-deauth.sh` | ✅ Done |
| Host & AP isolation test | `pentest/03-isolation.sh` | ✅ Done |
| VPN kill-switch test | `pentest/04-vpn-killswitch.sh` | ✅ Done |
| Stealth features test | `pentest/05-stealth.sh` | ✅ Done |
| MITM & traffic analysis | `pentest/06-mitm.sh` | ✅ Done |
| Orchestrator + HTML report | `pentest/run-all.sh` | ✅ Done |
| Shared helpers | `pentest/common.sh` | ✅ Done |

---

## Quick Start

```bash
# 1. Install dependencies
sudo ./vwifi/setup-vwifi.sh deps

# 2. Load virtual radios + udev rules
sudo ./vwifi/setup-vwifi.sh up

# 3. Start routerd dengan config testing
sudo routerd -c ./vwifi/routerd-test.conf start

# 4. Connect client virtual
sudo ./vwifi/vwifi-client.sh up

# 5. Run semua pentest
sudo ./pentest/run-all.sh

# 6. Teardown
sudo ./vwifi/setup-vwifi.sh down
```

---

## 1. Latar Belakang & Konteks

### 1.1 Kenapa perlu virtual Wi-Fi adapter?

| Kendala | Dampak |
|---------|--------|
| `routerd` memakai `wlan0` sebagai uplink & `ap0` sebagai AP | Interface tidak bisa dimasukkan ke monitor mode bersamaan |
| `ISOLATE_HOST=true` & AP isolation aktif | Host tidak bisa scan/menyerang client AP secara langsung |
| Client fisik (HP) tidak selalu aktif | Testing handshake/deauth/MITM jadi tidak konsisten |
| Tidak ada adapter fisik kedua | Tidak bisa packet injection / monitor terpisah |

**Solusi:** `mac80211_hwsim` kernel module — 3 virtual radio (AP + client + attacker) murni software.

### 1.2 Gap Analysis (dari rencana awal)

Gap yang ditemukan dan sudah di-address dalam implementasi:

| Gap | Masalah | Solusi yang Diimplementasikan |
|----|---------|-------------------------------|
| Konflik nama interface | hwsim bisa clash dengan wlan0 fisik | `70-vwifi.rules` pin nama vwlan0/1/2 |
| INTERFACE_STA=auto | routerd bisa grab hwsim sebagai uplink | `routerd-test.conf` eksplisit `INTERFACE_STA=vwlan0` |
| Script client.sh SSID salah | SSID "BOASH-VOKASI" bukan SSID routerd | `vwifi-client.sh` pakai `routerd-test` |
| Internet uplink untuk virtual AP | hwsim tidak punya koneksi internet | veth bridge (veth-host ↔ veth-ap) NAT ke wlan0 |
| Limitasi hwsim (no RF) | Signal sempurna, tidak realistis | Didokumentasikan di sini — masih valid untuk test software features |
| CI/GitHub Actions | Container tidak punya hwsim | Gunakan self-hosted runner (didokumentasikan di bawah) |

---

## 2. Arsitektur Implementasi

```
┌──────────────────────────────────────────────────────────────────┐
│                     MESIN HOST (archlinux)                       │
│                                                                  │
│  [RTL8822CE phy0] wlan0 ─── internet uplink (tidak disentuh)    │
│       │                                                          │
│  [veth-host] ─────── NAT → wlan0 (internet forwarding)          │
│       │                                                          │
│  mac80211_hwsim (3 virtual radios — udev pinned):               │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  [hwsim0] vwlan0 ── routerd AP (vap0) ch=6               │  │
│  │      │             hostapd + dnsmasq                      │  │
│  │      │             subnet: 192.168.99.0/24                │  │
│  │      │             uplink: veth-ap → veth-host → wlan0    │  │
│  │                                                            │  │
│  │  [hwsim1] vwlan1 ── wpa_supplicant (legit client)         │  │
│  │                     IP: 192.168.99.x (DHCP)               │  │
│  │                                                            │  │
│  │  [hwsim2] vwlan2 ── airmon-ng (monitor mode)              │  │
│  │                     airodump-ng / aireplay-ng             │  │
│  └───────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

---

## 3. Opsi Implementasi (Ringkasan)

### Opsi A — `mac80211_hwsim` ← YANG DIPAKAI
- Kernel module simulasi 3+ virtual radio
- Monitor mode + packet injection penuh
- Didukung semua tool aircrack-ng suite
- **Limitasi:** No RF propagation — signal selalu sempurna

### Opsi B — monitor interface di kartu fisik
- Tidak dipakai — berisiko memutus uplink routerd aktif

### Opsi C — Network namespace + veth
- Dipakai sebagai suplemen untuk isolation testing saja

### Opsi D — QEMU/KVM + USB passthrough
- Tidak relevan — tidak ada adapter fisik kedua

---

## 4. Dependencies

```bash
# Install semua sekaligus:
sudo ./vwifi/setup-vwifi.sh deps

# Manual:
sudo pacman -S --needed \
    aircrack-ng \
    wpa_supplicant \
    hashcat \
    tcpdump \
    nmap \
    dsniff \
    wireshark-cli \
    hcxtools \
    hcxdumptool \
    wordlists
```

---

## 5. Test Coverage

### 5.1 Wireless Security
| Test | Script | Checks |
|------|--------|--------|
| WPA2 handshake capture | `01-handshake.sh capture` | airodump + deauth trigger |
| WPA2 crack dengan wordlist | `01-handshake.sh crack` | aircrack-ng + hashcat |
| Deauth attack (single) | `02-deauth.sh single` | disconnect + reconnect |
| Deauth flood (DoS) | `02-deauth.sh flood` | broadcast deauth 30s |
| WPA3/MFP resistance | `02-deauth.sh test-mfp` | deauth diabaikan jika MFP |

### 5.2 Network & Host Isolation
| Test | Script | Expected |
|------|--------|---------|
| Host ports blocked | `03-isolation.sh host` | BLOCKED: 22,80,443,8080 |
| DNS allowed | `03-isolation.sh host` | ALLOWED: 53 |
| AP isolation antar client | `03-isolation.sh ap` | BLOCKED jika aktif |
| Internet via NAT | `03-isolation.sh internet` | ALLOWED |

### 5.3 VPN Kill-Switch
| Test | Script | Expected |
|------|--------|---------|
| Internet saat VPN aktif | `04-vpn-killswitch.sh` | REACHABLE |
| Internet saat VPN mati | `04-vpn-killswitch.sh` | BLOCKED |
| DNS leak saat VPN mati | `04-vpn-killswitch.sh` | BLOCKED |
| Restore setelah VPN aktif | `04-vpn-killswitch.sh` | REACHABLE |

### 5.4 Stealth Features
| Test | Script | Checks |
|------|--------|--------|
| RANDOM_MAC (LAA bit) | `05-stealth.sh mac` | bit 1 octet-1 set |
| TTL spoofing | `05-stealth.sh ttl` | TTL=64 di outgoing |
| Subnet RFC1918 | `05-stealth.sh subnet` | 10/172.16/192.168 |
| IPv6 leak | `05-stealth.sh ipv6` | no global IPv6 addr |
| Hidden SSID | `05-stealth.sh ssid` | tidak visible scan |

### 5.5 MITM & Traffic Analysis
| Test | Script | Checks |
|------|--------|--------|
| ARP spoofing | `06-mitm.sh arp` | arp table tidak berubah |
| DNS sniffing | `06-mitm.sh dns` | queries visible di AP |
| HTTP interception | `06-mitm.sh sniff` | plaintext traffic |

---

## 6. Limitasi hwsim yang Perlu Dipahami

1. **No RF propagation** — Semua radio saling "dengar" dengan signal -30 dBm.
   Deauth attack 100% berhasil di hwsim (tidak realistis di dunia nyata).

2. **Tidak ada channel contention** — Tidak ada interferensi antar channel.

3. **Tidak ada hidden terminal problem** — Semua node saling visible.

4. **Tujuan test tetap valid:** Yang ditest adalah fitur **software** routerd
   (iptables rules, VPN kill-switch, isolation, MAC randomization), bukan
   behavior RF fisik.

---

## 7. CI/CD Integration

GitHub Actions default runner tidak support `mac80211_hwsim`. Opsi:

### Self-hosted runner (recommended)
```yaml
# .github/workflows/pentest.yml
runs-on: self-hosted
# Runner harus di mesin fisik dengan kernel support hwsim
```

### Container dengan kernel module mount
```bash
# Hanya bekerja jika host kernel sudah load hwsim
docker run --privileged --net=host \
    -v /sys/bus/platform/devices/mac80211_hwsim:/sys/bus/platform/devices/mac80211_hwsim \
    routerd-pentest
```

Untuk saat ini, pentest dijalankan manual dengan `./pentest/run-all.sh`.

---

## 8. Catatan Legal & Etika

- Testing **hanya** terhadap sistem & jaringan **milik sendiri**.
- **Jangan** gunakan terhadap jaringan/device orang lain (UU ITE Indonesia).
- Environment ini terisolasi (hwsim virtual — tidak transmit RF ke udara).
- Hapus rule sudoers testing setelah selesai.
- Ganti password `testpass123` di `routerd-test.conf` setelah testing.

---

## 9. File Structure

```
routerd/
├── vwifi/
│   ├── 70-vwifi.rules          # udev rules untuk pin nama interface
│   ├── setup-vwifi.sh          # load/unload hwsim, install udev, veth bridge
│   ├── vwifi-client.sh         # connect vwlan1 sebagai client
│   └── routerd-test.conf       # routerd config untuk testing
│
└── pentest/
    ├── common.sh               # shared helpers & logging
    ├── run-all.sh              # orchestrator + HTML report generator
    ├── 01-handshake.sh         # WPA2 handshake capture & crack
    ├── 02-deauth.sh            # deauthentication attack
    ├── 03-isolation.sh         # host & AP isolation verification
    ├── 04-vpn-killswitch.sh    # VPN kill-switch test
    ├── 05-stealth.sh           # random MAC, TTL, IPv6, subnet tests
    └── 06-mitm.sh              # ARP spoof, DNS sniff, HTTP intercept
```
