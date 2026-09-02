#!/usr/bin/env bash
#
# routerd installer
#
# Usage: sudo ./install.sh [flags]
#
# Flags:
#   --with-deps      Install all system package dependencies automatically
#   --update-config  Overwrite /etc/routerd.conf with latest sample config
#   --enable         Enable & start routerd as a systemd service after install
#   --warp-setup     Auto-generate Cloudflare WARP WireGuard profile after install
#   --gen-keys       Generate a WireGuard keypair in /etc/routerd/ for custom VPN setup
#
# Examples:
#   sudo ./install.sh --with-deps --enable
#   sudo ./install.sh --with-deps --enable --warp-setup
#   sudo ./install.sh --with-deps --gen-keys   # for custom WireGuard VPS setup

set -euo pipefail

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
UPDATE_CONFIG=0
WITH_DEPS=0
ENABLE_SERVICE=0
WARP_SETUP=0
GEN_KEYS=0

for arg in "$@"; do
    [[ "$arg" == "--with-deps" ]]      && WITH_DEPS=1
    [[ "$arg" == "--update-config" ]]  && UPDATE_CONFIG=1
    [[ "$arg" == "--enable" ]]         && ENABLE_SERVICE=1
    [[ "$arg" == "--warp-setup" ]]     && WARP_SETUP=1
    [[ "$arg" == "--gen-keys" ]]       && GEN_KEYS=1
done

# ---------------------------------------------------------------------------
# Root check
# ---------------------------------------------------------------------------
if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    echo "error: this script must be run as root (use: sudo ./install.sh)" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# Dependency installation
# ---------------------------------------------------------------------------
install_deps() {
    echo "==> detecting package manager & installing dependencies"

    if command -v pacman >/dev/null 2>&1; then
        echo "==> detected Arch Linux / Manjaro (pacman)"
        pacman -Sy --needed --noconfirm \
            hostapd dnsmasq iw wireless-regdb wireguard-tools openresolv \
            iptables iproute2 procps-ng go

    elif command -v apt-get >/dev/null 2>&1; then
        echo "==> detected Debian / Ubuntu / Mint (apt)"
        apt-get update -qq
        DEBIAN_FRONTEND=noninteractive apt-get install -y \
            hostapd dnsmasq iw wireless-regdb wireguard-tools \
            openresolv resolvconf iptables iproute2 procps go

    elif command -v dnf >/dev/null 2>&1; then
        echo "==> detected Fedora / RHEL / Rocky / Alma (dnf)"
        # openresolv is not in default repos — install wireguard-tools which
        # pulls resolvconf on Fedora. Fallback: systemd-resolved handles DNS.
        dnf install -y \
            hostapd dnsmasq iw wireless-regdb wireguard-tools \
            iptables iproute procps-ng go
        # Try openresolv from EPEL or best-effort
        dnf install -y openresolv 2>/dev/null || \
            echo "    note: openresolv not found in repos — wg-quick will use resolvconf fallback"

    elif command -v yum >/dev/null 2>&1; then
        echo "==> detected RHEL / CentOS (yum)"
        yum install -y \
            hostapd dnsmasq iw wireless-regdb wireguard-tools \
            iptables iproute procps-ng go
        yum install -y openresolv 2>/dev/null || \
            echo "    note: openresolv not found — install manually if wg-quick DNS fails"

    elif command -v zypper >/dev/null 2>&1; then
        echo "==> detected openSUSE (zypper)"
        zypper install -y \
            hostapd dnsmasq iw wireless-regdb wireguard-tools \
            iptables iproute2 procps go
        zypper install -y openresolv 2>/dev/null || \
            echo "    note: openresolv not found — install manually if wg-quick DNS fails"

    elif command -v apk >/dev/null 2>&1; then
        echo "==> detected Alpine Linux (apk)"
        apk add --no-cache \
            hostapd dnsmasq iw wireless-tools wireguard-tools \
            iptables iproute2 procps openresolv go

    else
        echo "error: no supported package manager found (pacman, apt, dnf, yum, zypper, apk)" >&2
        exit 1
    fi

    echo "==> dependencies installed"
}

[[ "$WITH_DEPS" -eq 1 ]] && install_deps

# ---------------------------------------------------------------------------
# Build & install binary + systemd unit + NM rule
# ---------------------------------------------------------------------------
cd "$(dirname "$0")"

echo "==> building routerd"
make build

echo "==> installing files"
make install

# ---------------------------------------------------------------------------
# Systemd network manager rules — keep ap0 unmanaged
# ---------------------------------------------------------------------------
echo "==> configuring network manager rules for ap0"

# NetworkManager rule (if NM is present)
if command -v nmcli >/dev/null 2>&1 || [ -d /etc/NetworkManager/conf.d ]; then
    install -Dm644 90-routerd.conf /etc/NetworkManager/conf.d/90-routerd.conf
    echo "    installed /etc/NetworkManager/conf.d/90-routerd.conf (NetworkManager)"
fi

# systemd-networkd rule — prevents networkd from managing ap0
if systemctl is-active --quiet systemd-networkd 2>/dev/null || \
   systemctl is-enabled --quiet systemd-networkd 2>/dev/null; then
    mkdir -p /etc/systemd/network
    cat > /etc/systemd/network/10-routerd-ap0.network <<'NETEOF'
# routerd: keep ap0 unmanaged by systemd-networkd
[Match]
Name=ap0

[Link]
Unmanaged=yes
NETEOF
    echo "    installed /etc/systemd/network/10-routerd-ap0.network (systemd-networkd)"
fi

# ---------------------------------------------------------------------------
# Create runtime directory with correct permissions
# ---------------------------------------------------------------------------
echo "==> creating /run/routerd runtime directory"
install -dm700 -o root -g root /run/routerd || true

# Persist across reboots via tmpfiles.d
if [[ -d /etc/tmpfiles.d ]]; then
    echo "==> installing tmpfiles.d rule (persists /run/routerd across reboots)"
    cat > /etc/tmpfiles.d/routerd.conf <<'EOF'
# routerd runtime directory
d /run/routerd 0700 root root -
EOF
fi

# ---------------------------------------------------------------------------
# Config dirs + files
# ---------------------------------------------------------------------------
echo "==> configuring /etc/routerd.conf"
if [[ ! -f /etc/routerd.conf ]] || [[ "$UPDATE_CONFIG" -eq 1 ]]; then
    install -Dm600 -o root -g root routerd.conf.example /etc/routerd.conf
    echo "    installed /etc/routerd.conf"
else
    echo "    /etc/routerd.conf already exists (use --update-config to overwrite)"
fi

echo "==> configuring /etc/routerd/vpn.conf"
install -dm700 -o root -g root /etc/routerd
if [[ ! -f /etc/routerd/vpn.conf ]] || [[ "$UPDATE_CONFIG" -eq 1 ]]; then
    install -Dm600 -o root -g root vpn.conf.example /etc/routerd/vpn.conf
    echo "    installed /etc/routerd/vpn.conf"
else
    echo "    /etc/routerd/vpn.conf already exists (use --update-config to overwrite)"
fi

# ---------------------------------------------------------------------------
# Optional: Generate WireGuard keypair
# ---------------------------------------------------------------------------
if [[ "$GEN_KEYS" -eq 1 ]]; then
    if ! command -v wg >/dev/null 2>&1; then
        echo "warning: wg not found — cannot generate keys (install wireguard-tools first)" >&2
    else
        echo "==> generating WireGuard keypair in /etc/routerd/"
        PRIVKEY=$(wg genkey)
        PUBKEY=$(echo "$PRIVKEY" | wg pubkey)
        echo "$PRIVKEY" > /etc/routerd/privatekey
        echo "$PUBKEY"  > /etc/routerd/publickey
        chmod 600 /etc/routerd/privatekey
        chmod 644 /etc/routerd/publickey

        # Pre-fill PrivateKey into vpn.conf
        if grep -q "# PrivateKey" /etc/routerd/vpn.conf; then
            sed -i "s|# PrivateKey = YOUR_PRIVATE_KEY_HERE|PrivateKey = ${PRIVKEY}|" /etc/routerd/vpn.conf
            echo "    PrivateKey auto-filled in /etc/routerd/vpn.conf"
        fi

        echo "    Private key: /etc/routerd/privatekey"
        echo "    Public key:  /etc/routerd/publickey"
        echo "    Public key value: ${PUBKEY}"
        echo "    → Paste this public key into your WireGuard server's [Peer] section"
        echo "    → Then fill in Endpoint and AllowedIPs in /etc/routerd/vpn.conf"
        echo "    → Set ENABLE_VPN=true in /etc/routerd.conf when ready"
    fi
fi

# ---------------------------------------------------------------------------
# Optional: Cloudflare WARP auto-setup
# ---------------------------------------------------------------------------
if [[ "$WARP_SETUP" -eq 1 ]]; then
    echo "==> running routerd warp-setup (Cloudflare WARP)"
    if command -v routerd >/dev/null 2>&1; then
        routerd warp-setup && \
            echo "    WARP profile generated at /etc/routerd/vpn.conf" && \
            echo "    → Set ENABLE_VPN=true in /etc/routerd.conf to activate" || \
            echo "warning: warp-setup failed — run 'sudo routerd warp-setup' manually"
    else
        echo "warning: routerd binary not found in PATH yet — run 'sudo routerd warp-setup' manually"
    fi
fi

# ---------------------------------------------------------------------------
# Systemd
# ---------------------------------------------------------------------------
echo "==> reloading systemd"
systemctl daemon-reload

if [[ "$ENABLE_SERVICE" -eq 1 ]]; then
    echo "==> enabling & starting routerd service"
    systemctl enable --now routerd
    echo "    routerd enabled and started"
fi

# ---------------------------------------------------------------------------
# Post-install summary
# ---------------------------------------------------------------------------
echo
echo "=========================================================================="
echo "               routerd Installation Complete"
echo "=========================================================================="
echo
echo "  Binary:    /usr/local/bin/routerd"
echo "  Config:    /etc/routerd.conf"
echo "  VPN conf:  /etc/routerd/vpn.conf"
echo "  Service:   /etc/systemd/system/routerd.service"
echo "  NM rule:   /etc/NetworkManager/conf.d/90-routerd.conf"
echo
echo "─── Quick Start ──────────────────────────────────────────────────────────"
echo
echo "  1. Edit your Wi-Fi config:"
echo "       sudo nano /etc/routerd.conf"
echo "       → Set SSID and PASSWORD"
echo
echo "  2. VPN Setup (choose one):"
echo
echo "     A) Cloudflare WARP (free, one-command):"
echo "          sudo routerd warp-setup"
echo "          sudo nano /etc/routerd.conf  → set ENABLE_VPN=true"
echo
echo "     B) Custom WireGuard VPS / Mullvad / ProtonVPN:"
echo "          sudo nano /etc/routerd/vpn.conf  → fill PrivateKey, Endpoint, PublicKey"
echo "          sudo nano /etc/routerd.conf      → set ENABLE_VPN=true"
echo "       (generate keys: sudo ./install.sh --gen-keys)"
echo
echo "     C) Skip VPN (plain AP mode):"
echo "          Leave ENABLE_VPN=false in /etc/routerd.conf"
echo
echo "  3. Start:"
echo "       sudo systemctl start routerd"
echo "       sudo routerd status"
echo
echo "  4. Enable on boot:"
echo "       sudo systemctl enable --now routerd"
echo
echo "  Dashboard: sudo routerd dashboard  (opens at http://AP-gateway-ip:8080)"
echo "==========================================================================="
