#!/usr/bin/env bash
#
# routerd installer
#
# Usage: sudo ./install.sh
#
# Installs the routerd binary, systemd unit, sample config and a
# NetworkManager rule that keeps the virtual AP interface unmanaged.

set -euo pipefail

cd "$(dirname "$0")"

make install

echo "==> configuring /etc/routerd.conf (first run only)"
if [[ ! -f /etc/routerd.conf ]]; then
    install -Dm600 -o root -g root routerd.conf.example /etc/routerd.conf
    echo "    created /etc/routerd.conf from the sample"
else
    echo "    /etc/routerd.conf already exists, leaving it untouched"
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
