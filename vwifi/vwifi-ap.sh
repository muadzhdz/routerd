#!/usr/bin/env bash
# =============================================================================
# vwifi-ap.sh — Buat AP pentest di vwlan0 (hwsim) tanpa stop routerd production
# =============================================================================
# Masalah: vwlan1/vwlan2 (hwsim phy1/2/3) tidak bisa komunikasi dengan ap0
#          yang ada di phy0 (RTL8822CE fisik).
#
# Solusi: Jalankan hostapd TERPISAH di vwlan0 (hwsim phy1) dengan config
#         yang mirror /etc/routerd.conf — SSID, password, channel sama.
#         vwlan1 bisa connect karena sama-sama hwsim.
#         routerd production (ap0) tetap jalan tidak terganggu.
#
# Usage:
#   sudo ./vwifi-ap.sh up      # start AP pentest di vwlan0
#   sudo ./vwifi-ap.sh down    # stop AP pentest
#   sudo ./vwifi-ap.sh status  # cek status
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

AP_IFACE="vwlan0"      # hwsim phy1 — bisa komunikasi dengan vwlan1/vwlan2
AP_VIRTUAL="vtest0"    # virtual AP interface di atas vwlan0
RUNDIR="/tmp/vwifi-ap"
HOSTAPD_CONF="$RUNDIR/hostapd.conf"
DNSMASQ_CONF="$RUNDIR/dnsmasq.conf"
HOSTAPD_PID="$RUNDIR/hostapd.pid"
DNSMASQ_PID="$RUNDIR/dnsmasq.pid"
HOSTAPD_LOG="$RUNDIR/hostapd.log"
DNSMASQ_LOG="$RUNDIR/dnsmasq.log"
DNSMASQ_LEASES="$RUNDIR/dnsmasq.leases"

# Pentest AP config — baca dari /etc/routerd.conf untuk mirror production
PENTEST_SUBNET="192.168.88.0/24"
PENTEST_GW="192.168.88.1"
PENTEST_CHANNEL="6"  # 2.4GHz — lebih mudah untuk airodump test

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

log()  { echo -e "${BOLD}[vwifi-ap]${NC} $*"; }
ok()   { echo -e "${GREEN}[  OK  ]${NC} $*"; }
warn() { echo -e "${YELLOW}[ WARN ]${NC} $*"; }
err()  { echo -e "${RED}[ FAIL ]${NC} $*"; exit 1; }
info() { echo -e "${CYAN}[ INFO ]${NC} $*"; }

check_root() { [[ $EUID -eq 0 ]] || err "Butuh root: sudo $0 $*"; }
iface_exists() { ip link show "$1" &>/dev/null; }

# Baca credentials dari production config
get_ssid()     { grep "^SSID=" /etc/routerd.conf 2>/dev/null | cut -d= -f2 | tr -d '"' || echo "routerd-pentest"; }
get_password() { grep "^PASSWORD=" /etc/routerd.conf 2>/dev/null | cut -d= -f2 | tr -d '"' || echo ""; }
get_country()  { grep "^COUNTRY=" /etc/routerd.conf 2>/dev/null | cut -d= -f2 || echo "ID"; }

cmd_up() {
    check_root

    local ssid password country
    ssid=$(get_ssid)
    password=$(get_password)
    country=$(get_country)

    log "Starting pentest AP: SSID=$ssid ch=$PENTEST_CHANNEL"

    # Cek prerequisites
    iface_exists "$AP_IFACE" || err "$AP_IFACE tidak ditemukan. Jalankan: sudo vwifi/setup-vwifi.sh up"
    command -v hostapd  &>/dev/null || err "hostapd tidak ditemukan"
    command -v dnsmasq  &>/dev/null || err "dnsmasq tidak ditemukan"

    mkdir -p "$RUNDIR"
    cmd_down_silent

    # Buat virtual AP interface di atas vwlan0 (sama PHY dengan vwlan1/vwlan2)
    info "Membuat AP interface $AP_VIRTUAL di atas $AP_IFACE..."
    if iface_exists "$AP_VIRTUAL"; then
        iw dev "$AP_VIRTUAL" del 2>/dev/null || true
    fi
    iw dev "$AP_IFACE" interface add "$AP_VIRTUAL" type __ap
    ip link set "$AP_IFACE" up
    ip link set "$AP_VIRTUAL" up
    ok "$AP_VIRTUAL dibuat (PHY sama dengan vwlan1/vwlan2)"

    # Assign gateway IP
    ip addr add "${PENTEST_GW}/24" dev "$AP_VIRTUAL" 2>/dev/null || true
    ok "Gateway: $PENTEST_GW"

    # Generate hostapd.conf
    info "Membuat hostapd config..."
    cat > "$HOSTAPD_CONF" << EOF
interface=$AP_VIRTUAL
driver=nl80211
ssid=$ssid
hw_mode=g
channel=$PENTEST_CHANNEL
country_code=$country
ieee80211n=1
wmm_enabled=1
max_num_sta=8
EOF

    if [[ -n "$password" ]]; then
        cat >> "$HOSTAPD_CONF" << EOF
auth_algs=1
wpa=2
wpa_key_mgmt=WPA-PSK
wpa_pairwise=CCMP
rsn_pairwise=CCMP
wpa_passphrase=$password
EOF
    fi
    chmod 600 "$HOSTAPD_CONF"
    ok "hostapd.conf dibuat (SSID=$ssid, ch=$PENTEST_CHANNEL)"

    # Generate dnsmasq.conf
    info "Membuat dnsmasq config..."
    cat > "$DNSMASQ_CONF" << EOF
port=53
no-resolv
server=1.1.1.1
interface=$AP_VIRTUAL
bind-interfaces
listen-address=$PENTEST_GW
dhcp-range=192.168.88.10,192.168.88.50,255.255.255.0,12h
dhcp-option=option:router,$PENTEST_GW
dhcp-option=option:dns-server,$PENTEST_GW
dhcp-authoritative
dhcp-leasefile=$DNSMASQ_LEASES
EOF
    chmod 600 "$DNSMASQ_CONF"
    ok "dnsmasq.conf dibuat"

    # Start hostapd
    info "Starting hostapd..."
    hostapd -B -P "$HOSTAPD_PID" "$HOSTAPD_CONF" > "$HOSTAPD_LOG" 2>&1 || \
        err "hostapd gagal start. Cek: cat $HOSTAPD_LOG"

    # Poll sampai AP-ENABLED
    local deadline=$((SECONDS + 10))
    while [[ $SECONDS -lt $deadline ]]; do
        sleep 0.5
        grep -q "AP-ENABLED" "$HOSTAPD_LOG" 2>/dev/null && break
        pgrep -F "$HOSTAPD_PID" &>/dev/null || err "hostapd mati saat startup. Cek: cat $HOSTAPD_LOG"
    done
    grep -q "AP-ENABLED" "$HOSTAPD_LOG" || err "hostapd timeout. Cek: cat $HOSTAPD_LOG"
    ok "hostapd: AP-ENABLED di $AP_VIRTUAL"

    # Start dnsmasq
    info "Starting dnsmasq..."
    dnsmasq --keep-in-foreground --conf-file="$DNSMASQ_CONF" \
        --pid-file="$DNSMASQ_PID" --log-facility="$DNSMASQ_LOG" &
    sleep 1
    pgrep -F "$DNSMASQ_PID" &>/dev/null && ok "dnsmasq: started" || \
        warn "dnsmasq mungkin tidak jalan. Cek: cat $DNSMASQ_LOG"

    # NAT: forward dari AP pentest ke internet via wlan0
    info "Setup NAT (vwlan0 → wlan0)..."
    echo 1 > /proc/sys/net/ipv4/ip_forward
    iptables -t nat -C POSTROUTING -s "$PENTEST_SUBNET" -o wlan0 -j MASQUERADE 2>/dev/null || \
        iptables -t nat -A POSTROUTING -s "$PENTEST_SUBNET" -o wlan0 -j MASQUERADE
    iptables -C FORWARD -i "$AP_VIRTUAL" -o wlan0 -j ACCEPT 2>/dev/null || \
        iptables -A FORWARD -i "$AP_VIRTUAL" -o wlan0 -j ACCEPT
    iptables -C FORWARD -i wlan0 -o "$AP_VIRTUAL" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || \
        iptables -A FORWARD -i wlan0 -o "$AP_VIRTUAL" -m state --state RELATED,ESTABLISHED -j ACCEPT
    ok "NAT aktif: $PENTEST_SUBNET → wlan0"

    # Update state file untuk pentest scripts
    info "Update pentest state..."
    local ap_mac
    ap_mac=$(cat "/sys/class/net/$AP_VIRTUAL/address" 2>/dev/null || echo "")
    cat > "$RUNDIR/pentest-state" << EOF
SSID=$ssid
INTERFACE_AP=$AP_VIRTUAL
CHANNEL=$PENTEST_CHANNEL
SUBNET=$PENTEST_SUBNET
AP_MAC=$ap_mac
EOF
    ok "State saved: $RUNDIR/pentest-state"

    echo ""
    ok "Pentest AP READY!"
    echo ""
    info "SSID:      $ssid"
    info "Interface: $AP_VIRTUAL (PHY: vwlan0 hwsim)"
    info "Channel:   $PENTEST_CHANNEL (2.4GHz)"
    info "Subnet:    $PENTEST_SUBNET"
    info "Gateway:   $PENTEST_GW"
    info "MAC:       $ap_mac"
    echo ""
    info "Langkah selanjutnya:"
    echo "  1. Connect client:  sudo vwifi/vwifi-client.sh up"
    echo "  2. Run pentest:     sudo pentest/run-all.sh"
}

cmd_down_silent() {
    # Stop hostapd
    if [[ -f "$HOSTAPD_PID" ]]; then
        local pid; pid=$(cat "$HOSTAPD_PID" 2>/dev/null || echo "")
        [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
        rm -f "$HOSTAPD_PID"
    fi
    pkill -f "hostapd.*$HOSTAPD_CONF" 2>/dev/null || true

    # Stop dnsmasq
    if [[ -f "$DNSMASQ_PID" ]]; then
        local pid; pid=$(cat "$DNSMASQ_PID" 2>/dev/null || echo "")
        [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
        rm -f "$DNSMASQ_PID"
    fi
    pkill -f "dnsmasq.*$DNSMASQ_CONF" 2>/dev/null || true

    # Cleanup iptables
    iptables -t nat -D POSTROUTING -s "$PENTEST_SUBNET" -o wlan0 -j MASQUERADE 2>/dev/null || true
    iptables -D FORWARD -i "$AP_VIRTUAL" -o wlan0 -j ACCEPT 2>/dev/null || true
    iptables -D FORWARD -i wlan0 -o "$AP_VIRTUAL" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true

    # Remove virtual interface
    iface_exists "$AP_VIRTUAL" && iw dev "$AP_VIRTUAL" del 2>/dev/null || true
}

cmd_down() {
    check_root
    log "Stopping pentest AP..."
    cmd_down_silent
    ok "Pentest AP stopped"
}

cmd_status() {
    echo -e "\n${BOLD}=== Pentest AP Status ===${NC}"
    if iface_exists "$AP_VIRTUAL"; then
        local mac ch
        mac=$(cat "/sys/class/net/$AP_VIRTUAL/address" 2>/dev/null || echo "unknown")
        ch=$(iw dev "$AP_VIRTUAL" info 2>/dev/null | grep "channel" | awk '{print $2}' || echo "?")
        echo -e "  ${GREEN}$AP_VIRTUAL${NC}: UP mac=$mac ch=$ch"
        local ssid; ssid=$(get_ssid)
        echo -e "  SSID: $ssid | Gateway: $PENTEST_GW"
    else
        echo -e "  ${RED}$AP_VIRTUAL${NC}: not running"
        echo "  Jalankan: sudo $0 up"
    fi
    echo ""
    echo "hostapd: $(pgrep -f "hostapd.*$HOSTAPD_CONF" &>/dev/null && echo "running" || echo "stopped")"
    echo "dnsmasq: $(pgrep -f "dnsmasq.*$DNSMASQ_CONF" &>/dev/null && echo "running" || echo "stopped")"
}

main() {
    local cmd="${1:-help}"
    case "$cmd" in
        up)     cmd_up ;;
        down)   check_root; cmd_down ;;
        status) cmd_status ;;
        help|-h|--help)
            echo "Usage: sudo $0 [up|down|status]"
            echo ""
            echo "Buat AP pentest di vwlan0 (hwsim) yang bisa dijangkau vwlan1/vwlan2."
            echo "Routerd production (ap0) tetap jalan tidak terganggu."
            ;;
        *) echo "Unknown: $cmd"; exit 1 ;;
    esac
}

main "$@"
