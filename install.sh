#!/usr/bin/env bash
#
# routerd installer
#
# Usage: sudo ./install.sh
#        sudo ./install.sh --with-deps
#        sudo ./install.sh --update-config
#
# Installs the routerd binary, systemd unit, sample config, NetworkManager rule,
# and runtime dependencies (hostapd, dnsmasq, iw, wireless-regdb, wireguard-tools, openresolv).

set -euo pipefail

UPDATE_CONFIG=0
WITH_DEPS=0
for arg in "$@"; do
    [[ "$arg" == "--with-deps" ]] && WITH_DEPS=1
    [[ "$arg" == "--update-config" ]] && UPDATE_CONFIG=1
done

install_deps() {
    echo "==> detecting package manager & installing dependencies"
    if command -v pacman >/dev/null 2>&1; then
        echo "==> detected Arch Linux / Manjaro (pacman)"
        pacman -Sy --needed hostapd dnsmasq iw wireless-regdb wireguard-tools openresolv
    elif command -v apt-get >/dev/null 2>&1; then
        echo "==> detected Debian / Ubuntu / Mint (apt)"
        apt-get update
        apt-get install -y hostapd dnsmasq iw wireless-regdb wireguard-tools openresolv resolvconf
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
        apk add hostapd dnsmasq iw wireless-regdb wireguard-tools
    else
        echo "error: no supported package manager found (pacman, apt, dnf, yum, zypper, apk)" >&2
        exit 1
    fi
}

[[ "$WITH_DEPS" -eq 1 ]] && install_deps

cd "$(dirname "$0")"

make install

echo "==> configuring /etc/routerd.conf"
if [[ ! -f /etc/routerd.conf ]] || [[ "$UPDATE_CONFIG" -eq 1 ]]; then
    install -Dm600 -o root -g root routerd.conf.example /etc/routerd.conf
    echo "    updated /etc/routerd.conf with latest sample config"
else
    echo "    /etc/routerd.conf already exists, leaving it untouched (use --update-config to overwrite)"
fi

echo "==> configuring /etc/routerd/vpn.conf"
if [[ ! -f /etc/routerd/vpn.conf ]] || [[ "$UPDATE_CONFIG" -eq 1 ]]; then
    install -Dm600 -o root -g root vpn.conf.example /etc/routerd/vpn.conf
    echo "    updated /etc/routerd/vpn.conf with WireGuard sample profile"
else
    echo "    /etc/routerd/vpn.conf already exists, leaving it untouched"
fi

echo "==> reloading systemd"
systemctl daemon-reload

echo
echo "=========================================================================="
echo "                   routerd Installation Successful                        "
echo "=========================================================================="
echo
echo "1. Basic Wi-Fi Configuration:"
echo "   Edit configuration file:  sudo nano /etc/routerd.conf"
echo "   Set SSID & PASSWORD as needed."
echo
echo "2. WireGuard VPN Setup (Optional - for Uncensored Privacy):"
echo "   Option A (Cloudflare WARP template):"
echo "     sudo routerd warp-setup"
echo "     (Fill in keys in /etc/routerd/vpn.conf and set ENABLE_VPN=true)"
echo
echo "   Option B (Manual Key Generation for VPS/Custom WireGuard):"
echo "     wg genkey | tee privatekey | wg pubkey > publickey"
echo "     Paste PrivateKey into /etc/routerd/vpn.conf [Interface] section."
echo
echo "3. Daemon Control Commands:"
echo "   Start access point:       sudo systemctl start routerd"
echo "   Check live status:        sudo routerd status"
echo "   Start automatically:      sudo systemctl enable --now routerd"
echo "   Stop access point:        sudo systemctl stop routerd"
echo "   Reload configuration:     sudo systemctl reload routerd"
echo "=========================================================================="

