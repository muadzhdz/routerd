#!/usr/bin/env bash
# =============================================================================
# vwifi-client.sh — Connect vwlan1 ke AP yang sedang aktif (production/test)
# =============================================================================
# Membaca SSID dari /run/routerd/state dan password dari /etc/routerd.conf
# sehingga selalu sync dengan AP yang sedang berjalan — tidak hardcoded.
#
# Usage:
#   sudo ./vwifi-client.sh up      # connect ke AP aktif
#   sudo ./vwifi-client.sh down    # disconnect, cleanup
#   sudo ./vwifi-client.sh status  # cek status koneksi
#   sudo ./vwifi-client.sh ping    # ping test ke gateway & internet
# =============================================================================

set -euo pipefail

IFACE="vwlan1"
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

# ── Baca SSID & password dari routerd yang sedang berjalan ───────────────────
get_ap_credentials() {
    # Prioritas: pentest AP (vwifi-ap.sh) → production routerd
    local ssid
    ssid=$(grep "^SSID=" /tmp/vwifi-ap/pentest-state 2>/dev/null | cut -d= -f2 ||            grep "^SSID=" /run/routerd/state 2>/dev/null | cut -d= -f2 || echo "")
    if [[ -z "$ssid" ]]; then
        err "Tidak ada AP aktif. Jalankan: sudo vwifi/vwifi-ap.sh up  atau  sudo routerd start"
    fi

    # Password dari /etc/routerd.conf
    local password
    password=$(grep "^PASSWORD=" /etc/routerd.conf 2>/dev/null | cut -d= -f2 | tr -d '"' || echo "")

    echo "$ssid" "$password"
}

# ── Baca channel AP yang aktif ───────────────────────────────────────────────
get_ap_channel() {
    grep "^CHANNEL=" /tmp/vwifi-ap/pentest-state 2>/dev/null | cut -d= -f2 ||     grep "^CHANNEL=" /run/routerd/state 2>/dev/null | cut -d= -f2 || echo "6"
}

get_ap_interface() {
    grep "^INTERFACE_AP=" /tmp/vwifi-ap/pentest-state 2>/dev/null | cut -d= -f2 ||     grep "^INTERFACE_AP=" /run/routerd/state 2>/dev/null | cut -d= -f2 || echo "ap0"
}

# ── Command: up ───────────────────────────────────────────────────────────────
cmd_up() {
    check_root

    # Baca credentials dari routerd yang aktif
    local creds ssid password
    creds=$(get_ap_credentials)
    ssid=$(echo "$creds" | awk '{print $1}')
    password=$(echo "$creds" | awk '{print $2}')

    local ap_iface channel
    ap_iface=$(get_ap_interface)
    channel=$(get_ap_channel)

    log "Connecting $IFACE → SSID=$ssid (AP: $ap_iface, ch=$channel)"

    # Verifikasi prerequisites
    iface_exists "$IFACE" || err "$IFACE tidak ditemukan. Jalankan: sudo vwifi/setup-vwifi.sh up"
    command -v wpa_supplicant &>/dev/null || err "wpa_supplicant tidak ditemukan. Jalankan: sudo vwifi/setup-vwifi.sh deps"

    # Stop existing connections
    cmd_down_silent

    # Pastikan interface UP
    ip link set "$IFACE" up
    sleep 0.5

    # Set channel yang sama dengan AP (hwsim bisa semua channel)
    if [[ "$channel" != "auto" ]] && [[ -n "$channel" ]]; then
        info "Set $IFACE ke channel $channel (sama dengan AP)..."
        iw dev "$IFACE" set channel "$channel" 2>/dev/null || true
    fi

    # Buat wpa_supplicant config
    info "Membuat wpa_supplicant config untuk SSID=$ssid..."

    if [[ -n "$password" ]]; then
        # WPA2 dengan password
        cat > "$WPA_CONF" << EOF
ctrl_interface=$WPA_CTRL
ctrl_interface_group=root
update_config=1

network={
    ssid="$ssid"
    psk="$password"
    key_mgmt=WPA-PSK
    proto=WPA2
    pairwise=CCMP
    group=CCMP
    priority=10
}
EOF
    else
        # Open network (no password)
        cat > "$WPA_CONF" << EOF
ctrl_interface=$WPA_CTRL
ctrl_interface_group=root
update_config=1

network={
    ssid="$ssid"
    key_mgmt=NONE
    priority=10
}
EOF
    fi
    chmod 600 "$WPA_CONF"
    ok "wpa_supplicant config dibuat (SSID=$ssid)"

    # Start wpa_supplicant
    info "Starting wpa_supplicant pada $IFACE..."
    wpa_supplicant -B \
        -i "$IFACE" \
        -c "$WPA_CONF" \
        -P "$WPA_PID" \
        -f "$LOG" \
        -D nl80211,wext
    ok "wpa_supplicant started (PID: $(cat "$WPA_PID" 2>/dev/null || echo "unknown"))"

    # Tunggu asosiasi
    info "Menunggu koneksi ke AP $ssid..."
    local timeout=30 elapsed=0
    while [[ $elapsed -lt $timeout ]]; do
        local state
        state=$(wpa_cli -i "$IFACE" status 2>/dev/null | grep "wpa_state" | cut -d= -f2 || echo "")
        if [[ "$state" == "COMPLETED" ]]; then
            ok "Terkoneksi ke AP $ssid"
            break
        fi
        sleep 1
        ((elapsed++))
        echo -ne "\r  Menunggu... ${elapsed}s/${timeout}s (state=${state:-scanning})"
    done
    echo ""

    if [[ $elapsed -ge $timeout ]]; then
        warn "Timeout menunggu koneksi."
        warn "Kemungkinan penyebab:"
        warn "  1. vwlan1 dan ap0 beda PHY — hwsim harus pada PHY yang sama"
        warn "  2. Channel mismatch: ap0=$channel, cek dengan: iw dev ap0 info"
        warn "  3. Password salah di /etc/routerd.conf"
        warn "Log: tail $LOG"
        return 1
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
        warn "Belum dapat IP — DHCP mungkin butuh waktu lebih"
        warn "Cek: ip addr show $IFACE"
    fi

    cmd_status
}

# ── Command: down ─────────────────────────────────────────────────────────────
cmd_down() {
    check_root
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
    # Kill any lingering wpa_supplicant on this iface
    pkill -f "wpa_supplicant.*$IFACE" 2>/dev/null || true

    ip addr flush dev "$IFACE" 2>/dev/null || true
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

    # AP info
    local ssid ap_iface channel
    ssid=$(grep "^SSID=" /run/routerd/state 2>/dev/null | cut -d= -f2 || echo "unknown")
    ap_iface=$(get_ap_interface)
    channel=$(get_ap_channel)
    echo -e "  Target AP:  ${CYAN}$ssid${NC} ($ap_iface, ch=$channel)"

    echo -e "\n${BOLD}wpa_supplicant:${NC}"
    if [[ -f "$WPA_PID" ]] && kill -0 "$(cat "$WPA_PID" 2>/dev/null)" 2>/dev/null; then
        wpa_cli -i "$IFACE" status 2>/dev/null | \
            grep -E "wpa_state|ssid|bssid|freq|ip_address" | sed 's/^/  /'
    else
        echo -e "  ${RED}wpa_supplicant tidak berjalan${NC}"
    fi

    echo -e "\n${BOLD}IP Address:${NC}"
    ip addr show "$IFACE" | grep -E "inet |link" | sed 's/^/  /'

    echo -e "\n${BOLD}Routes:${NC}"
    ip route show dev "$IFACE" 2>/dev/null | sed 's/^/  /' || echo "  (none)"
}

# ── Command: ping ─────────────────────────────────────────────────────────────
cmd_ping() {
    echo -e "\n${BOLD}=== Connectivity Test ===${NC}\n"

    local subnet gw
    subnet=$(grep "^SUBNET=" /run/routerd/state 2>/dev/null | cut -d= -f2 || echo "")
    if [[ -n "$subnet" ]]; then
        gw="${subnet%.*}.1"
    else
        gw=$(ip route show dev "$IFACE" | grep "default" | grep -oP 'via \K[\d.]+' | head -1 || echo "")
    fi

    info "Gateway: $gw"

    if ping -I "$IFACE" -c3 -W2 "$gw" &>/dev/null; then
        ok "Gateway $gw reachable"
    else
        warn "Gateway $gw tidak reachable"
    fi

    if ping -I "$IFACE" -c3 -W3 8.8.8.8 &>/dev/null; then
        ok "Internet (8.8.8.8) reachable"
    else
        warn "Internet tidak reachable"
    fi
}

# ── Main ──────────────────────────────────────────────────────────────────────
main() {
    local cmd="${1:-help}"
    echo "" >> "$LOG"
    echo "=== vwifi-client $(date '+%Y-%m-%d %H:%M:%S') cmd=$cmd ===" >> "$LOG"
    case "$cmd" in
        up)     cmd_up ;;
        down)   check_root; cmd_down ;;
        status) cmd_status ;;
        ping)   cmd_ping ;;
        help|--help|-h)
            echo "Usage: sudo $0 [up|down|status|ping]"
            echo ""
            echo "  Otomatis baca SSID dari /run/routerd/state"
            echo "  dan password dari /etc/routerd.conf"
            echo "  — selalu sync dengan AP yang sedang berjalan"
            ;;
        *) echo "Unknown: $cmd  |  Usage: sudo $0 [up|down|status|ping]"; exit 1 ;;
    esac
}

main "$@"
