#!/bin/bash
# uninstall.sh
# Removes the network watcher service and scripts from the router.
# Usage: bash uninstall.sh

set -euo pipefail

SERVICE_NAME="network-watcher"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
SCRIPT_DIR="/data/scripts/ipip"

echo "=== Network Watcher Uninstaller ==="

# 1. Stop and disable the service
if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
    echo "[1/3] Stopping ${SERVICE_NAME}..."
    systemctl stop "$SERVICE_NAME"
else
    echo "[1/3] Service not running, skipping stop"
fi

if systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null; then
    echo "       Disabling ${SERVICE_NAME}..."
    systemctl disable "$SERVICE_NAME"
fi

# 2. Remove service file
if [ -f "$SERVICE_FILE" ]; then
    echo "[2/3] Removing ${SERVICE_FILE}"
    rm -f "$SERVICE_FILE"
    systemctl daemon-reload
    systemctl reset-failed "$SERVICE_NAME" 2>/dev/null || true
else
    echo "[2/3] Service file not found, skipping"
fi

# 3. Remove scripts (keep config.env by default)
echo "[3/3] Removing scripts..."
rm -f "${SCRIPT_DIR}/network-watcher.sh"
rm -f "${SCRIPT_DIR}/on-network-change.sh"
rm -f "${SCRIPT_DIR}/configure.sh"
rm -f "/tmp/network-change.lock"
rm -f "/tmp/network-change.pending"

# Clean up empty dir, but only if nothing else is in there
if [ -d "$SCRIPT_DIR" ] && [ -z "$(ls -A "$SCRIPT_DIR")" ]; then
    rmdir "$SCRIPT_DIR"
    echo "       Removed empty ${SCRIPT_DIR}/"
else
    echo "       Kept ${SCRIPT_DIR}/ (other files present)"
fi

echo ""
echo "=== Uninstall complete ==="
