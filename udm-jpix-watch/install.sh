#!/bin/sh
set -eu

cd "$(dirname "$0")"
dest=/data/scripts/udm-jpix-watch
units=/etc/systemd/system
pin=$dest/endpoint.conf

if [ "$(id -u)" != 0 ] || [ "$#" -gt 1 ]; then
    echo "Run as root: ./install.sh <local-tunnel-IPv6/128> (omit the address on reinstall)" >&2
    exit 1
fi
if [ -f "$pin" ]; then
    candidate=$(cat "$pin")
    if [ "$#" -eq 1 ] && [ "$1" != "$candidate" ]; then
        echo "A different endpoint is already pinned in $pin. Review it before reinstalling." >&2
        exit 1
    fi
elif [ "$#" -eq 1 ]; then
    candidate=$1
else
    echo "Supply the local IPv6 tunnel endpoint including /128 on first install." >&2
    exit 1
fi

# Verify both the live tunnel and the delegated prefix before enabling writes.
/usr/bin/python3 -IB reconcile.py check "$candidate"
install -d -m 0700 "$dest"
stage=$(mktemp -d "$dest/.install.XXXXXX")
trap 'rm -rf "$stage"' EXIT
if [ ! -f "$pin" ]; then
    printf '%s\n' "$candidate" > "$stage/endpoint.conf"
    chmod 0600 "$stage/endpoint.conf"
    mv "$stage/endpoint.conf" "$pin"
fi
install -m 0600 reconcile.py "$stage/reconcile.py"
mv "$stage/reconcile.py" "$dest/reconcile.py"
install -m 0644 udm-jpix-watch.service udm-jpix-watch.path "$units/"
systemctl daemon-reload
systemctl enable --now udm-jpix-watch.path udm-jpix-watch.service
