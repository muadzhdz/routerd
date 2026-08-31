#!/usr/bin/env bash
# =============================================================================
# setup-vwifi.sh — Virtual Wi-Fi Environment untuk routerd pentest
# =============================================================================
# Menginisialisasi 3 virtual radio menggunakan mac80211_hwsim:
#   vwlan0 → AP radio  (dipakai routerd dengan routerd-test.conf)
#   vwlan1 → Client radio (wpa_supplicant — legit client)
#   vwlan2 → Attacker/monitor radio (aircrack-ng suite)
#
# Usage:
#   sudo ./setup-vwifi.sh up      # load hwsim, install udev rules, bring up radios
#   sudo ./setup-vwifi.sh down    # teardown semua, unload hwsim
#   sudo ./setup-vwifi.sh status  # tampilkan status radios
#   sudo ./setup-vwifi.sh deps    # install semua dependencies yang dibutuhkan
#
# Requirements: Arch Linux, kernel dengan mac80211_hwsim support
# Author: routerd pentest environment
# =============================================================================

set -euo pipefail

# ── Konstanta ─────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
UDEV_RULES_SRC="$SCRIPT_DIR/70-vwifi.rules"
UDEV_RULES_DEST="/etc/udev/rules.d/70-vwifi.rules"
HWSIM_MODULE="mac80211_hwsim"
NUM_RADIOS=3
VWLAN_AP="vwlan0"
VWLAN_CLIENT="vwlan1"
VWLAN_MONITOR="vwlan2"
LOG_FILE="/tmp/vwifi-setup.log"

# ── Colors ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# ── Helpers ───────────────────────────────────────────────────────────────────
log()     { echo -e "${BOLD}[vwifi]${NC} $*" | tee -a "$LOG_FILE"; }
ok()      { echo -e "${GREEN}[  OK  ]${NC} $*" | tee -a "$LOG_FILE"; }
warn()    { echo -e "${YELLOW}[ WARN ]${NC} $*" | tee -a "$LOG_FILE"; }
err()     { echo -e "${RED}[ FAIL ]${NC} $*" | tee -a "$LOG_FILE"; exit 1; }
info()    { echo -e "${CYAN}[ INFO ]${NC} $*" | tee -a "$LOG_FILE"; }
header()  { echo -e "\n${BOLD}${BLUE}═══ $* ═══${NC}\n" | tee -a "$LOG_FILE"; }

check_root() {
    [[ $EUID -eq 0 ]] || err "Script ini harus dijalankan sebagai root (sudo $0 $*)"
}

iface_exists() {
    ip link show "$1" &>/dev/null
}

hwsim_loaded() {
    lsmod | grep -q "mac80211_hwsim"
}

# ── Command: deps ─────────────────────────────────────────────────────────────
cmd_deps() {
    header "Installing Dependencies"

    local pkgs=()

    # Check setiap tool
    declare -A tools=(
        ["wpa_supplicant"]="wpa_supplicant"
        ["hashcat"]="hashcat"
        ["tcpdump"]="tcpdump"
        ["dsniff"]="dsniff"
        ["hcxdumptool"]="hcxdumptool"
        ["hcxtools"]="hcxtools"
    )

    for tool in "${!tools[@]}"; do
        if ! command -v "$tool" &>/dev/null; then
            warn "$tool tidak ditemukan, menambahkan ke install list"
            pkgs+=("${tools[$tool]}")
        else
            ok "$tool sudah terinstall"
        fi
    done

    # Aircrack suite
    for tool in aircrack-ng airodump-ng aireplay-ng airmon-ng packetforge-ng; do
        if ! command -v "$tool" &>/dev/null; then
            pkgs+=("aircrack-ng")
            break
        fi
    done

    # Wordlist — rockyou.txt tidak ada di Arch official repo, download manual
    if [[ ! -f /usr/share/wordlists/rockyou.txt ]]; then
        if [[ -f /usr/share/wordlists/rockyou.txt.gz ]]; then
            info "Extracting rockyou.txt.gz..."
            gunzip -k /usr/share/wordlists/rockyou.txt.gz
            ok "rockyou.txt extracted"
        else
            warn "rockyou.txt tidak ditemukan di /usr/share/wordlists/"
            info "Mendownload rockyou.txt (~134MB)..."
            mkdir -p /usr/share/wordlists
            if command -v curl &>/dev/null; then
                curl -L --progress-bar \
                    "https://github.com/brannondorsey/naive-hashcat/releases/download/data/rockyou.txt" \
                    -o /usr/share/wordlists/rockyou.txt && \
                ok "rockyou.txt downloaded ke /usr/share/wordlists/" || \
                warn "Download gagal. Manual: curl -L <url> -o /usr/share/wordlists/rockyou.txt"
            else
                warn "curl tidak tersedia. Install: sudo pacman -S curl"
                info "Atau download manual dari: https://github.com/brannondorsey/naive-hashcat/releases/download/data/rockyou.txt"
            fi
        fi
    else
        ok "rockyou.txt ada di /usr/share/wordlists/"
    fi

    if [[ ${#pkgs[@]} -eq 0 ]]; then
        ok "Semua dependencies sudah terinstall!"
        return 0
    fi

    # Hapus duplikat
    local unique_pkgs
    mapfile -t unique_pkgs < <(printf '%s\n' "${pkgs[@]}" | sort -u)

    info "Menginstall: ${unique_pkgs[*]}"
    pacman -S --noconfirm --needed "${unique_pkgs[@]}"

    ok "Dependencies berhasil diinstall"
}

# ── Command: up ──────────────────────────────────────────────────────────────
cmd_up() {
    header "Starting Virtual Wi-Fi Environment"

    # 0. Pastikan routerd tidak memakai interface yang akan konflik
    info "Mengecek routerd state..."
    if systemctl is-active --quiet routerd; then
        local sta
        sta=$(grep "^INTERFACE_STA=" /run/routerd/state 2>/dev/null | cut -d= -f2)
        if [[ "$sta" == "wlan0" ]] || [[ -z "$sta" ]]; then
            ok "routerd aktif dengan STA=$sta — aman (tidak akan konflik dengan hwsim)"
        else
            warn "routerd menggunakan STA=$sta — pastikan tidak konflik"
        fi
    else
        info "routerd tidak aktif"
    fi

    # 1. Unload hwsim jika sudah terlanjur dimuat (bersihkan state lama)
    if hwsim_loaded; then
        warn "mac80211_hwsim sudah ter-load, unload dulu untuk memastikan clean state..."
        modprobe -r "$HWSIM_MODULE" || err "Gagal unload $HWSIM_MODULE. Coba: sudo pkill wpa_supplicant && sudo modprobe -r mac80211_hwsim"
        sleep 1
    fi

    # 2. Install udev rules untuk pin nama interface
    if [[ ! -f "$UDEV_RULES_SRC" ]]; then
        err "File udev rules tidak ditemukan: $UDEV_RULES_SRC\nJalankan script dari direktori vwifi/"
    fi

    if [[ ! -f "$UDEV_RULES_DEST" ]] || ! diff -q "$UDEV_RULES_SRC" "$UDEV_RULES_DEST" &>/dev/null; then
        info "Menginstall udev rules ke $UDEV_RULES_DEST..."
        cp "$UDEV_RULES_SRC" "$UDEV_RULES_DEST"
        udevadm control --reload-rules
        ok "udev rules installed & reloaded"
    else
        ok "udev rules sudah up-to-date"
    fi

    # 3. Load mac80211_hwsim dengan 3 radios
    info "Loading mac80211_hwsim dengan $NUM_RADIOS radios..."
    modprobe "$HWSIM_MODULE" radios="$NUM_RADIOS"
    sleep 2  # Beri waktu kernel buat interfaces

    # 4. Verifikasi interfaces terbuat
    # udev rules harusnya sudah rename ke vwlan0/1/2
    # Kalau udev belum sempat, rename manual
    _ensure_interface_names

    # 5. Pastikan interfaces UP
    for iface in "$VWLAN_AP" "$VWLAN_CLIENT" "$VWLAN_MONITOR"; do
        if iface_exists "$iface"; then
            ip link set "$iface" up
            ok "$iface: UP"
        else
            err "Interface $iface tidak ditemukan setelah hwsim load!"
        fi
    done

    # 6. Set vwlan2 ke monitor mode untuk attacker
    info "Set $VWLAN_MONITOR ke monitor mode..."
    # Matikan dulu sebelum ganti mode
    ip link set "$VWLAN_MONITOR" down
    iw dev "$VWLAN_MONITOR" set type monitor
    ip link set "$VWLAN_MONITOR" up
    ok "$VWLAN_MONITOR: monitor mode aktif"

    # 7. Setup veth bridge untuk internet forwarding (optional)
    _setup_veth_bridge

    # 8. Status akhir
    cmd_status
    echo ""
    ok "Virtual Wi-Fi environment READY!"
    info "Langkah selanjutnya:"
    echo "  1. Jalankan routerd:  sudo routerd -c ${PROJECT_DIR}/vwifi/routerd-test.conf start"
    echo "  2. Connect client:    sudo ${SCRIPT_DIR}/vwifi-client.sh up"
    echo "  3. Jalankan pentest:  sudo ${PROJECT_DIR}/pentest/run-all.sh"
    echo "  4. Teardown:          sudo $0 down"
}

# ── Helper: pastikan nama interface sesuai ────────────────────────────────────
_ensure_interface_names() {
    info "Memverifikasi nama interface hwsim..."

    # Cari semua interface yang dibuat hwsim (bukan wlan0/ap0 fisik)
    local phys_phy
    phys_phy=$(iw dev wlan0 info 2>/dev/null | grep wiphy | awk '{print $2}' || echo "0")

    local hwsim_ifaces=()
    while IFS= read -r line; do
        local iface phy
        iface=$(echo "$line" | awk '{print $2}')
        # Skip interface fisik & ap0 routerd
        [[ "$iface" == "wlan0" ]] && continue
        [[ "$iface" == "ap0" ]] && continue
        [[ "$iface" == vwlan* ]] && continue  # sudah di-rename udev
        hwsim_ifaces+=("$iface")
    done < <(iw dev | grep "Interface")

    # Jika udev sudah bekerja, vwlan0/1/2 sudah ada
    local already_named=0
    iface_exists "$VWLAN_AP"      && ((already_named++)) || true
    iface_exists "$VWLAN_CLIENT"  && ((already_named++)) || true
    iface_exists "$VWLAN_MONITOR" && ((already_named++)) || true

    if [[ $already_named -eq 3 ]]; then
        ok "udev auto-rename berhasil: vwlan0, vwlan1, vwlan2"
        return 0
    fi

    # Manual rename fallback jika udev belum sempat
    if [[ ${#hwsim_ifaces[@]} -lt 3 ]]; then
        # Tunggu sedikit lagi dan coba lagi
        sleep 2
        hwsim_ifaces=()
        while IFS= read -r line; do
            local iface
            iface=$(echo "$line" | awk '{print $2}')
            [[ "$iface" == "wlan0" || "$iface" == "ap0" ]] && continue
            [[ "$iface" == vwlan* ]] && continue
            hwsim_ifaces+=("$iface")
        done < <(iw dev | grep "Interface")
    fi

    if [[ ${#hwsim_ifaces[@]} -lt 3 ]]; then
        err "Hanya ${#hwsim_ifaces[@]} hwsim interface ditemukan, butuh 3.\nInterface yang ada: ${hwsim_ifaces[*]:-none}\nPastikan mac80211_hwsim loaded dan tidak ada konflik."
    fi

    warn "udev rename belum bekerja, melakukan manual rename..."
    local names=("$VWLAN_AP" "$VWLAN_CLIENT" "$VWLAN_MONITOR")
    for i in 0 1 2; do
        local src="${hwsim_ifaces[$i]}"
        local dst="${names[$i]}"
        if ! iface_exists "$dst"; then
            ip link set "$src" down
            ip link set "$src" name "$dst"
            ok "Renamed $src → $dst"
        fi
    done
}

# ── Helper: setup veth bridge untuk internet forwarding ──────────────────────
_setup_veth_bridge() {
    info "Setup veth bridge untuk internet forwarding ke wlan0..."

    # Buat veth pair: veth-vwifi (host side) ↔ veth-ap (ap side)
    if ! iface_exists "veth-host"; then
        ip link add veth-host type veth peer name veth-ap
        ok "veth pair dibuat: veth-host ↔ veth-ap"
    else
        ok "veth pair sudah ada"
    fi

    ip link set veth-host up
    ip link set veth-ap up

    # Assign IP ke veth-host sebagai gateway untuk vwlan0 chain
    # vwlan0 (routerd AP) akan NAT ke veth-ap → veth-host → wlan0
    if ! ip addr show veth-host | grep -q "10.99.0.1"; then
        ip addr add 10.99.0.1/30 dev veth-host 2>/dev/null || true
        ip addr add 10.99.0.2/30 dev veth-ap  2>/dev/null || true
        ok "IP assigned: veth-host=10.99.0.1, veth-ap=10.99.0.2"
    fi

    # Enable forwarding dari veth ke wlan0 (internet)
    echo 1 > /proc/sys/net/ipv4/ip_forward
    iptables -t nat -C POSTROUTING -o wlan0 -j MASQUERADE 2>/dev/null || \
        iptables -t nat -A POSTROUTING -o wlan0 -j MASQUERADE
    iptables -C FORWARD -i veth-host -o wlan0 -j ACCEPT 2>/dev/null || \
        iptables -A FORWARD -i veth-host -o wlan0 -j ACCEPT
    iptables -C FORWARD -i wlan0 -o veth-host -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || \
        iptables -A FORWARD -i wlan0 -o veth-host -m state --state RELATED,ESTABLISHED -j ACCEPT

    ok "Internet forwarding: veth-host → wlan0 aktif"
}

# ── Command: down ─────────────────────────────────────────────────────────────
cmd_down() {
    header "Tearing Down Virtual Wi-Fi Environment"

    # Stop wpa_supplicant jika masih jalan di vwlan1
    if pgrep -f "wpa_supplicant.*vwlan1" &>/dev/null; then
        info "Stopping wpa_supplicant di vwlan1..."
        pkill -f "wpa_supplicant.*vwlan1" || true
        sleep 1
        ok "wpa_supplicant stopped"
    fi

    # Release DHCP lease di vwlan1
    if command -v dhclient &>/dev/null; then
        dhclient -r vwlan1 2>/dev/null || true
    fi

    # Stop airodump/aireplay jika jalan
    pkill -f "airodump-ng" 2>/dev/null || true
    pkill -f "aireplay-ng"  2>/dev/null || true
    ok "Stopped aircrack processes"

    # Cleanup veth
    if iface_exists "veth-host"; then
        iptables -t nat -D POSTROUTING -o wlan0 -j MASQUERADE 2>/dev/null || true
        iptables -D FORWARD -i veth-host -o wlan0 -j ACCEPT 2>/dev/null || true
        iptables -D FORWARD -i wlan0 -o veth-host -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true
        ip link del veth-host 2>/dev/null || true
        ok "veth bridge removed"
    fi

    # Unload hwsim
    if hwsim_loaded; then
        info "Unloading mac80211_hwsim..."
        modprobe -r "$HWSIM_MODULE"
        sleep 1
        ok "mac80211_hwsim unloaded"
    else
        info "mac80211_hwsim tidak ter-load"
    fi

    # Hapus PID files
    rm -f /tmp/wpa_supplicant_vwlan1.pid
    rm -f /tmp/airodump_capture.*

    ok "Virtual Wi-Fi environment berhasil di-teardown"
}

# ── Command: status ───────────────────────────────────────────────────────────
cmd_status() {
    header "Virtual Wi-Fi Status"

    echo -e "${BOLD}Module:${NC}"
    if hwsim_loaded; then
        echo -e "  mac80211_hwsim: ${GREEN}loaded${NC}"
    else
        echo -e "  mac80211_hwsim: ${RED}not loaded${NC}"
    fi

    echo -e "\n${BOLD}Interfaces:${NC}"
    for iface in "$VWLAN_AP" "$VWLAN_CLIENT" "$VWLAN_MONITOR"; do
        if iface_exists "$iface"; then
            local state mode mac
            state=$(cat "/sys/class/net/$iface/operstate" 2>/dev/null || echo "unknown")
            mac=$(cat "/sys/class/net/$iface/address" 2>/dev/null || echo "unknown")
            mode=$(iw dev "$iface" info 2>/dev/null | grep "type" | awk '{print $2}' || echo "unknown")
            echo -e "  ${GREEN}$iface${NC}: state=$state mode=$mode mac=$mac"
        else
            echo -e "  ${RED}$iface${NC}: not found"
        fi
    done

    echo -e "\n${BOLD}veth bridge:${NC}"
    if iface_exists "veth-host"; then
        local ip_addr
        ip_addr=$(ip addr show veth-host 2>/dev/null | grep "inet " | awk '{print $2}' || echo "no IP")
        echo -e "  ${GREEN}veth-host${NC}: $ip_addr"
    else
        echo -e "  ${RED}veth-host${NC}: not found"
    fi

    echo -e "\n${BOLD}Wireless PHYs:${NC}"
    iw dev 2>/dev/null | grep -E "^phy|Interface|type" | head -30

    echo -e "\n${BOLD}wpa_supplicant:${NC}"
    if pgrep -f "wpa_supplicant.*vwlan1" &>/dev/null; then
        echo -e "  ${GREEN}running${NC} on vwlan1"
    else
        echo -e "  ${RED}not running${NC}"
    fi
}

# ── Main ──────────────────────────────────────────────────────────────────────
main() {
    local cmd="${1:-help}"

    # Log header
    echo "" >> "$LOG_FILE"
    echo "=== vwifi-setup $(date '+%Y-%m-%d %H:%M:%S') cmd=$cmd ===" >> "$LOG_FILE"

    case "$cmd" in
        up)
            check_root
            cmd_up
            ;;
        down)
            check_root
            cmd_down
            ;;
        status)
            cmd_status
            ;;
        deps)
            check_root
            cmd_deps
            ;;
        help|--help|-h)
            echo "Virtual Wi-Fi Pentest Environment untuk routerd"
            echo ""
            echo "Usage: sudo $0 <command>"
            echo ""
            echo "Commands:"
            echo "  up      Load hwsim, install udev rules, bring up 3 virtual radios"
            echo "  down    Teardown semua, unload hwsim"
            echo "  status  Tampilkan status radios & interfaces"
            echo "  deps    Install semua dependencies (wpa_supplicant, hashcat, dll)"
            echo ""
            echo "Interface mapping:"
            echo "  vwlan0  AP radio    → digunakan routerd (routerd-test.conf)"
            echo "  vwlan1  Client      → wpa_supplicant legit client"
            echo "  vwlan2  Monitor     → aircrack-ng attacker/sniffer"
            ;;
        *)
            echo "Unknown command: $cmd"
            echo "Usage: sudo $0 [up|down|status|deps]"
            exit 1
            ;;
    esac
}

main "$@"
