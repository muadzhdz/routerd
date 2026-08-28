#!/usr/bin/env bash
#
# routerd installer
#
# Usage: sudo ./install.sh
#        sudo ./install.sh --with-deps
#
# Installs the routerd binary, systemd unit, sample config and a
# NetworkManager rule that keeps the virtual AP interface unmanaged.
#
# --with-deps  also installs the runtime packages (hostapd, dnsmasq, iw,
#              wireless-regdb) using pacman or apt, whichever is present.

set -euo pipefail

UPDATE_CONFIG=0
WITH_DEPS=0
for arg in "$@"; do
    [[ "$arg" == "--with-deps" ]] && WITH_DEPS=1
    [[ "$arg" == "--update-config" ]] && UPDATE_CONFIG=1
done

install_deps() {
    echo "==> installing dependencies"
    if command -v pacman >/dev/null 2>&1; then
        pacman -Sy --needed hostapd dnsmasq iw wireless-regdb wireguard-tools
    elif command -v apt-get >/dev/null 2>&1; then
        apt-get update
        apt-get install -y hostapd dnsmasq iw wireless-regdb wireguard-tools
    else
        echo "error: no supported package manager found (pacman or apt-get)" >&2
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

echo "==> reloading systemd"
systemctl daemon-reload

echo
echo "routerd installed."
echo
echo "  Configure network name & password:  sudo nano /etc/routerd.conf"
echo "  Start the access point:             sudo systemctl start routerd"
echo "  Start on every boot:                sudo systemctl enable --now routerd"
echo "  Check status:                       sudo routerd status"
