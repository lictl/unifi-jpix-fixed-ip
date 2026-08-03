#!/bin/bash
# install.sh
# Run this on your UniFi router to deploy the network watcher service.
# Usage: bash install.sh

set -euo pipefail

SCRIPT_DIR="/data/scripts/ipip"
SERVICE_FILE="/etc/systemd/system/network-watcher.service"
INOTIFY_GO="/data/bin/inotify-go"
CONFIG_FILE="${SCRIPT_DIR}/config.env"
CONFIG_TEMPLATE="config.example.env"

echo "=== Network Watcher Installer ==="



# 1. Check inotify-go (recommended for persistence across UniFi OS upgrades)
echo "[1/5] Checking inotify-go..."
if [ ! -x "$INOTIFY_GO" ]; then
    echo "WARNING: inotify-go is missing at ${INOTIFY_GO}"
    echo ""
    echo "We recommend installing inotify-go to persist watcher behavior"
    echo "across UniFi OS upgrades."
    echo ""
    echo "After a UniFi OS upgrade, apt packages can be reset and"
    echo "inotify-tools may be removed. inotify-go helps avoid that issue."
    echo ""

    if [ -t 0 ]; then
        read -r -p "Continue anyway without inotify-go? [y/N]: " continue_without_inotify_go
        case "${continue_without_inotify_go}" in
            [yY]|[yY][eE][sS])
                echo "Continuing without inotify-go..."
                ;;
            *)
                echo "Aborted. Install inotify-go first, then rerun install.sh."
                exit 1
                ;;
        esac
    else
        echo "Aborted: non-interactive shell and inotify-go is missing."
        echo "Install inotify-go first, then rerun install.sh."
        exit 1
    fi
else
    echo "[1/5] inotify-go found at ${INOTIFY_GO}"
fi

# 2. Only install inotify-tools when the recommended inotify-go backend is
# unavailable and the user chose to continue with the fallback.
if [ -x "$INOTIFY_GO" ]; then
    echo "[2/5] Skipping inotify-tools (inotify-go is installed)"
elif ! command -v inotifywait &> /dev/null; then
    echo "[2/5] Installing inotify-tools fallback..."
    apt-get update -qq && apt-get install -y -qq inotify-tools
else
    echo "[2/5] inotify-tools fallback already installed"
fi

# 3. Deploy scripts
echo "[3/5] Deploying scripts to ${SCRIPT_DIR}/"
mkdir -p "$SCRIPT_DIR"
cp network-watcher.sh "$SCRIPT_DIR/"
cp on-network-change.sh "$SCRIPT_DIR/"
cp configure.sh "$SCRIPT_DIR/"
chmod +x "$SCRIPT_DIR"/*.sh

if [ ! -f "$CONFIG_FILE" ]; then
    cp "$CONFIG_TEMPLATE" "$CONFIG_FILE"
    chmod 600 "$CONFIG_FILE"
    echo "       Created config template: ${CONFIG_FILE}"
else
    echo "       Found existing config: ${CONFIG_FILE} (left unchanged)"
fi

# 4. Install systemd service
echo "[4/5] Installing systemd service..."
cp network-watcher.service "$SERVICE_FILE"
systemctl daemon-reload
systemctl enable network-watcher.service

# 5. Start the service
echo "[5/5] Starting network-watcher..."
systemctl restart network-watcher.service

echo ""
echo "=== Done ==="
echo ""
echo "Useful commands:"
echo "  systemctl status network-watcher     # Check service status"
echo "  journalctl -fu network-watcher       # Follow logs live"
echo "  journalctl -u network-watcher --since '1 hour ago'  # Recent logs"
echo ""
echo "Edit your custom logic in: ${SCRIPT_DIR}/on-network-change.sh"
echo "Edit IPIP settings in: ${CONFIG_FILE}"
