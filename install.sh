#!/usr/bin/env bash
#
# routerd installer
#
# Usage: sudo ./install.sh
#        sudo ./install.sh --with-deps
#        sudo ./install.sh --update-config
#        sudo ./install.sh --enable
#
# Installs the routerd binary, systemd unit, sample config, NetworkManager rule,
# and runtime dependencies (hostapd, dnsmasq, iw, wireless-regdb, wireguard-tools, openresolv).

set -euo pipefail

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
UPDATE_CONFIG=0
WITH_DEPS=0
ENABLE_SERVICE=0
for arg in "$@"; do
    [[ "$arg" == "--with-deps" ]]      && WITH_DEPS=1
    [[ "$arg" == "--update-config" ]]  && UPDATE_CONFIG=1
    [[ "$arg" == "--enable" ]]         && ENABLE_SERVICE=1
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
        pacman -Sy --needed --noconfirm hostapd dnsmasq iw wireless-regdb wireguard-tools openresolv
    elif command -v apt-get >/dev/null 2>&1; then
        echo "==> detected Debian / Ubuntu / Mint (apt)"
        apt-get update -qq
        DEBIAN_FRONTEND=noninteractive apt-get install -y \
            hostapd dnsmasq iw wireless-regdb wireguard-tools openresolv resolvconf
    elif command -v dnf >/dev/null 2>&1; then
        echo "==> detected Fedora / RHEL / Rocky / Alma (dnf)"
        dnf install -y hostapd dnsmasq iw wireless-regdb wireguard-tools
    elif command -v yum >/dev/null 2>&1; then
        echo "==> detected RHEL / CentOS (yum)"
        yum install -y hostapd dnsmasq iw wireless-regdb wireguard-tools
    elif command -v zypper >/dev/null 2>&1; then
        echo "==> detected openSUSE (zypper)"
        zypper install -y hostapd dnsmasq iw wireless-regdb wireguard-tools
    elif command -v apk >/dev/null 2>&1; then
        echo "==> detected Alpine Linux (apk)"
        apk add --no-cache hostapd dnsmasq iw wireless-regdb wireguard-tools
    else
        echo "error: no supported package manager found (pacman, apt, dnf, yum, zypper, apk)" >&2
        exit 1
    fi
}

[[ "$WITH_DEPS" -eq 1 ]] && install_deps

# ---------------------------------------------------------------------------
# Build & install binary + systemd unit + NM rule
# ---------------------------------------------------------------------------
cd "$(dirname "$0")"

make install

# ---------------------------------------------------------------------------
# Create runtime directory with correct permissions
# ---------------------------------------------------------------------------
echo "==> creating /run/routerd runtime directory"
install -dm700 -o root -g root /run/routerd || true

# Persist across reboots via tmpfiles.d
if [[ -d /etc/tmpfiles.d ]]; then
    echo "==> installing tmpfiles.d rule (persists /run/routerd across reboots)"
    cat >/etc/tmpfiles.d/routerd.conf <<'EOF'
# routerd runtime directory
d /run/routerd 0700 root root -
EOF
fi

# ---------------------------------------------------------------------------
# Main config
# ---------------------------------------------------------------------------
echo "==> configuring /etc/routerd.conf"
if [[ ! -f /etc/routerd.conf ]] || [[ "$UPDATE_CONFIG" -eq 1 ]]; then
    install -Dm600 -o root -g root routerd.conf.example /etc/routerd.conf
    echo "    updated /etc/routerd.conf with latest sample config"
else
    echo "    /etc/routerd.conf already exists, leaving it untouched (use --update-config to overwrite)"
fi

# ---------------------------------------------------------------------------
# VPN config dir + template
# ---------------------------------------------------------------------------
echo "==> configuring /etc/routerd/vpn.conf"
install -dm700 -o root -g root /etc/routerd
if [[ ! -f /etc/routerd/vpn.conf ]] || [[ "$UPDATE_CONFIG" -eq 1 ]]; then
    install -Dm600 -o root -g root vpn.conf.example /etc/routerd/vpn.conf
    echo "    updated /etc/routerd/vpn.conf with WireGuard sample profile"
else
    echo "    /etc/routerd/vpn.conf already exists, leaving it untouched"
fi

# ---------------------------------------------------------------------------
# Systemd
# ---------------------------------------------------------------------------
echo "==> reloading systemd"
systemctl daemon-reload

if [[ "$ENABLE_SERVICE" -eq 1 ]]; then
    echo "==> enabling & starting routerd service"
    systemctl enable --now routerd
    echo "    routerd service enabled and started"
fi

# ---------------------------------------------------------------------------
# Post-install summary
# ---------------------------------------------------------------------------
echo
echo "=========================================================================="
echo "                   routerd Installation Successful                        "
echo "=========================================================================="
echo
echo "Binary:   /usr/local/bin/routerd"
echo "Config:   /etc/routerd.conf"
echo "VPN:      /etc/routerd/vpn.conf"
echo "Service:  /etc/systemd/system/routerd.service"
echo "NM rule:  /etc/NetworkManager/conf.d/90-routerd.conf"
echo
echo "1. Basic Wi-Fi Configuration:"
echo "   sudo nano /etc/routerd.conf"
echo "   → Set SSID and PASSWORD"
echo
echo "2. WireGuard VPN Setup (optional):"
echo "   Option A — Cloudflare WARP (free, automated):"
echo "     sudo routerd warp-setup"
echo "     sudo nano /etc/routerd.conf  (set ENABLE_VPN=true)"
echo
echo "   Option B — Custom WireGuard VPS / Mullvad / ProtonVPN:"
echo "     wg genkey | tee privatekey | wg pubkey > publickey"
echo "     sudo nano /etc/routerd/vpn.conf  (fill in keys)"
echo "     sudo nano /etc/routerd.conf      (set ENABLE_VPN=true)"
echo
echo "3. Start & Control:"
echo "   Start now:            sudo systemctl start routerd"
echo "   Enable on boot:       sudo systemctl enable --now routerd"
echo "   Check status:         sudo routerd status"
echo "   View logs:            sudo routerd logs"
echo "   Web dashboard:        sudo routerd dashboard"
echo "   Stop:                 sudo systemctl stop routerd"
echo "   Reload config:        sudo systemctl reload routerd"
echo
echo "   TIP: Re-run with --enable to enable and start the service automatically:"
echo "        sudo ./install.sh --enable"
echo "=========================================================================="
