#!/bin/bash
# /data/scripts/ipip/on-network-change.sh
# Called by network-watcher.sh when the ubios state file changes.
# Includes debounce logic to avoid rapid-fire execution.

set -euo pipefail

LOCKFILE="/tmp/network-change.lock"
PENDING_FILE="/tmp/network-change.pending"
DEBOUNCE_SECONDS=5

log() {
    logger -t "network-change" "$1"
    echo "$(date '+%Y-%m-%d %H:%M:%S') [network-change] $1"
}

# Debounce and coalesce concurrent events. The first instance takes the lock
# immediately. Later instances mark a pending run and wait; once one waiter
# consumes the marker, any redundant waiters exit without another apply.
exec 9>"$LOCKFILE"
if ! flock -n 9; then
    touch "$PENDING_FILE"
    log "Debounce: recovery already running; queued a pending check"
    flock 9

    if [ ! -f "$PENDING_FILE" ]; then
        log "Debounce: pending check already handled"
        exit 0
    fi
fi
rm -f "$PENDING_FILE"

log "Network change detected, waiting ${DEBOUNCE_SECONDS}s debounce..."
sleep "$DEBOUNCE_SECONDS"

log "Executing network change actions"

# ============================================================
# ADD YOUR CUSTOM LOGIC BELOW
# ============================================================

# Example: Log current WAN IP
# WAN_IP=$(ip -4 addr show dev eth8 2>/dev/null | grep -oP 'inet \K[\d.]+' || echo "unknown")
# log "Current WAN IP: ${WAN_IP}"

# Example: Update dynamic DNS
# curl -s "https://your-ddns-provider.com/update?ip=${WAN_IP}" && \
#     log "DDNS updated" || log "DDNS update failed"

# Example: Restart a tunnel
# systemctl restart my-tunnel.service && \
#     log "Tunnel restarted" || log "Tunnel restart failed"

STATE_FILE="/data/udapi-config/ubios-udapi-server/ubios-udapi-server.state"

if grep -q "map_e_jpix" "$STATE_FILE"; then
    log "Network config reverted to map_e_jpix in state file, running configure.sh"
    if /data/scripts/ipip/configure.sh; then
        log "configure.sh completed successfully"
    else
        CONFIGURE_STATUS=$?
        log "configure.sh failed with exit code ${CONFIGURE_STATUS}"
        exit "$CONFIGURE_STATUS"
    fi
else
    log "Network config did not revert to map_e_jpix in state file, skipping"
fi

# ============================================================
# END CUSTOM LOGIC
# ============================================================

log "Network change actions completed"
