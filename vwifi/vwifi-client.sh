#!/usr/bin/env bash
# =============================================================================
# vwifi-client.sh — Connect vwlan1 sebagai legitimate client ke routerd-test AP
# =============================================================================
# Menggunakan wpa_supplicant untuk WPA2 authentication dan dhclient untuk DHCP.
#
# Usage:
#   sudo ./vwifi-client.sh up      # connect ke AP
#   sudo ./vwifi-client.sh down    # disconnect, cleanup
#   sudo ./vwifi-client.sh status  # cek status koneksi
#   sudo ./vwifi-client.sh ping    # ping test ke gateway & internet
#
# Requirements: vwlan1 harus sudah ada (jalankan setup-vwifi.sh up dulu)
# =============================================================================

set -euo pipefail

IFACE="vwlan1"
SSID="routerd-test"
PASSWORD="testpass123"
WPA_CONF="/tmp/vwifi-wpa.conf"
WPA_PID="/tmp/wpa_supplicant_vwlan1.pid"
WPA_CTRL="/tmp/wpa_ctrl_vwlan1"
DHCP_PID="/tmp/dhclient_vwlan1.pid"
DHCP_LEASE="/tmp/dhclient_vwlan1.lease"
LOG="/tmp/vwifi-client.log"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${BOLD}[client]${NC} $*" | tee -a "$LOG"; }
ok()   { echo -e "${GREEN}[  OK  ]${NC} $*" | tee -a "$LOG"; }
warn() { echo -e "${YELLOW}[ WARN ]${NC} $*" | tee -a "$LOG"; }
err()  { echo -e "${RED}[ FAIL ]${NC} $*" | tee -a "$LOG"; exit 1; }
info() { echo -e "${CYAN}[ INFO ]${NC} $*" | tee -a "$LOG"; }

check_root() { [[ $EUID -eq 0 ]] || err "Butuh root: sudo $0 $*"; }

iface_exists() { ip link show "$1" &>/dev/null; }

# ── Command: up ───────────────────────────────────────────────────────────────
cmd_up() {
    log "Connecting $IFACE → SSID=$SSID"

    # Verifikasi prerequisites
    iface_exists "$IFACE" || err "$IFACE tidak ditemukan. Jalankan: sudo setup-vwifi.sh up"
    command -v wpa_supplicant &>/dev/null || err "wpa_supplicant tidak ditemukan. Jalankan: sudo setup-vwifi.sh deps"
    command -v dhclient &>/dev/null || command -v dhcpcd &>/dev/null || \
        err "dhclient/dhcpcd tidak ditemukan. Install: sudo pacman -S dhclient"

    # Stop existing connections
    cmd_down_silent

    # Pastikan interface UP
    ip link set "$IFACE" up
    sleep 0.5

    # Buat wpa_supplicant config
    info "Membuat wpa_supplicant config..."
    cat > "$WPA_CONF" << EOF
ctrl_interface=$WPA_CTRL
ctrl_interface_group=root
update_config=1

network={
    ssid="$SSID"
    psk="$PASSWORD"
    key_mgmt=WPA-PSK
    proto=WPA2
    pairwise=CCMP
    group=CCMP
    priority=10
}
EOF
    chmod 600 "$WPA_CONF"
    ok "wpa_supplicant config dibuat"

    # Start wpa_supplicant
    info "Starting wpa_supplicant pada $IFACE..."
    wpa_supplicant -B \
        -i "$IFACE" \
        -c "$WPA_CONF" \
        -P "$WPA_PID" \
        -f "$LOG" \
        -D nl80211,wext
    ok "wpa_supplicant started (PID: $(cat "$WPA_PID" 2>/dev/null || echo "unknown"))"

    # Tunggu asosiasi — polling sampai COMPLETED atau timeout
    info "Menunggu koneksi ke AP..."
    local timeout=30
    local elapsed=0
    while [[ $elapsed -lt $timeout ]]; do
        local status
        status=$(wpa_cli -i "$IFACE" status 2>/dev/null | grep "wpa_state" | cut -d= -f2 || echo "UNKNOWN")
        if [[ "$status" == "COMPLETED" ]]; then
            ok "Terkoneksi ke AP $SSID"
            break
        fi
        sleep 1
        ((elapsed++))
        echo -ne "\r  Menunggu... ${elapsed}s/${timeout}s (state=$status)"
    done
    echo ""

    if [[ $elapsed -ge $timeout ]]; then
        warn "Timeout menunggu koneksi. Mungkin AP belum siap atau password salah."
        warn "Cek: wpa_cli -i $IFACE status"
        warn "Log: cat $LOG"
    fi

    # Request DHCP
    info "Requesting DHCP lease pada $IFACE..."
    if command -v dhclient &>/dev/null; then
        dhclient -v -pf "$DHCP_PID" -lf "$DHCP_LEASE" "$IFACE" 2>>"$LOG" &
        sleep 3
        ok "dhclient started"
    elif command -v dhcpcd &>/dev/null; then
        dhcpcd -b "$IFACE" 2>>"$LOG"
        sleep 3
        ok "dhcpcd started"
    fi

    # Verifikasi IP
    local ip
    ip=$(ip addr show "$IFACE" | grep "inet " | awk '{print $2}' || echo "")
    if [[ -n "$ip" ]]; then
        ok "IP address: $ip"
    else
        warn "Belum dapat IP address. Mungkin DHCP butuh waktu lebih lama."
        warn "Cek: ip addr show $IFACE"
    fi

    cmd_status
}

# ── Command: down ─────────────────────────────────────────────────────────────
cmd_down() {
    log "Disconnecting $IFACE..."
    cmd_down_silent
    ok "Client disconnected"
}

cmd_down_silent() {
    # Release DHCP
    if [[ -f "$DHCP_PID" ]]; then
        local pid
        pid=$(cat "$DHCP_PID" 2>/dev/null || echo "")
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            dhclient -r -pf "$DHCP_PID" "$IFACE" 2>/dev/null || true
        fi
        rm -f "$DHCP_PID"
    fi

    # Stop dhcpcd jika jalan
    dhcpcd -k "$IFACE" 2>/dev/null || true

    # Stop wpa_supplicant
    if [[ -f "$WPA_PID" ]]; then
        local pid
        pid=$(cat "$WPA_PID" 2>/dev/null || echo "")
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            wpa_cli -i "$IFACE" terminate 2>/dev/null || kill "$pid" 2>/dev/null || true
        fi
        rm -f "$WPA_PID"
    fi

    # Flush IP
    ip addr flush dev "$IFACE" 2>/dev/null || true

    # Cleanup files
    rm -f "$WPA_CONF" "$DHCP_LEASE"
    rm -rf "$WPA_CTRL"
}

# ── Command: status ───────────────────────────────────────────────────────────
cmd_status() {
    echo -e "\n${BOLD}=== Client Status: $IFACE ===${NC}"

    if ! iface_exists "$IFACE"; then
        echo -e "  Interface: ${RED}not found${NC}"
        return 1
    fi

    # wpa_supplicant status
    echo -e "\n${BOLD}wpa_supplicant:${NC}"
    if [[ -f "$WPA_PID" ]] && kill -0 "$(cat "$WPA_PID" 2>/dev/null)" 2>/dev/null; then
        local wpa_status bssid freq ssid
        wpa_status=$(wpa_cli -i "$IFACE" status 2>/dev/null || echo "error")
        echo "$wpa_status" | grep -E "wpa_state|ssid|bssid|freq|ip_address" | \
            sed 's/^/  /'
    else
        echo -e "  ${RED}wpa_supplicant tidak berjalan${NC}"
    fi

    # IP address
    echo -e "\n${BOLD}IP Address:${NC}"
    ip addr show "$IFACE" | grep -E "inet |link" | sed 's/^/  /'

    # Routes
    echo -e "\n${BOLD}Routes:${NC}"
    ip route show dev "$IFACE" 2>/dev/null | sed 's/^/  /' || echo "  (none)"
}

# ── Command: ping ─────────────────────────────────────────────────────────────
cmd_ping() {
    echo -e "\n${BOLD}=== Connectivity Test ===${NC}\n"

    local gw
    gw=$(ip route show dev "$IFACE" | grep "default\|192.168.99" | \
         grep -oP 'via \K[\d.]+' | head -1 || echo "")

    if [[ -z "$gw" ]]; then
        # Coba detect gateway dari lease atau ARP
        gw="192.168.99.1"
        warn "Gateway tidak ditemukan via routing, mencoba $gw"
    fi

    info "Ping gateway ($gw)..."
    if ping -I "$IFACE" -c3 -W2 "$gw" &>/dev/null; then
        ok "Gateway $gw reachable"
    else
        warn "Gateway $gw tidak reachable"
    fi

    info "Ping 8.8.8.8 (internet)..."
    if ping -I "$IFACE" -c3 -W3 8.8.8.8 &>/dev/null; then
        ok "Internet (8.8.8.8) reachable"
    else
        warn "Internet tidak reachable (VPN mungkin tidak aktif atau routing belum setup)"
    fi

    info "DNS test (8.8.8.8)..."
    if dig +short @8.8.8.8 google.com A &>/dev/null; then
        ok "DNS resolution bekerja"
    else
        warn "DNS resolution gagal"
    fi
}

# ── Main ──────────────────────────────────────────────────────────────────────
main() {
    local cmd="${1:-help}"
    echo "" >> "$LOG"
    echo "=== vwifi-client $(date '+%Y-%m-%d %H:%M:%S') cmd=$cmd ===" >> "$LOG"

    case "$cmd" in
        up)     check_root; cmd_up ;;
        down)   check_root; cmd_down ;;
        status) cmd_status ;;
        ping)   cmd_ping ;;
        help|--help|-h)
            echo "Usage: sudo $0 [up|down|status|ping]"
            echo ""
            echo "  up      Connect vwlan1 ke routerd-test AP"
            echo "  down    Disconnect dan cleanup"
            echo "  status  Tampilkan status koneksi"
            echo "  ping    Test konektivitas ke gateway & internet"
            ;;
        *)
            echo "Unknown: $cmd  |  Usage: sudo $0 [up|down|status|ping]"
            exit 1
            ;;
    esac
}

main "$@"
