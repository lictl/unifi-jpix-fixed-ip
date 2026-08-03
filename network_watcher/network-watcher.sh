#!/bin/bash
# /data/scripts/ipip/network-watcher.sh
# Watches the ubios-udapi-server state file for changes and triggers
# the on-network-change.sh script when modifications are detected.

set -euo pipefail

STATE_FILE_ORIG="/data/udapi-config/ubios-udapi-server/ubios-udapi-server.state"
STATE_FILE="$(readlink -f "$STATE_FILE_ORIG")"
CHANGE_SCRIPT="/data/scripts/ipip/on-network-change.sh"
INOTIFY_GO="/data/bin/inotify-go"

log() {
    logger -t "network-watcher" "$1"
    echo "$(date '+%Y-%m-%d %H:%M:%S') [network-watcher] $1"
}

log "Starting network watcher (PID: $$)"
log "Original path: ${STATE_FILE_ORIG}"
log "Resolved path: ${STATE_FILE}"

# Wait for the state file to exist (may not be ready immediately after boot)
if [ ! -f "$STATE_FILE" ]; then
    log "State file not found, waiting..."
    while [ ! -f "$STATE_FILE" ]; do
        sleep 5
    done
    log "State file found, proceeding"
fi

# Select the inotify backend. Prefer the bundled static inotify-go binary: it
# lives in /data (which survives UniFi OS firmware updates) and has no runtime
# dependencies. Fall back to inotifywait from inotify-tools if it isn't present.
if [ -x "$INOTIFY_GO" ]; then
    WATCHER_BIN="$INOTIFY_GO"
elif command -v inotifywait &> /dev/null; then
    WATCHER_BIN="inotifywait"
else
    log "ERROR: no inotify backend found. Place the inotify-go binary at ${INOTIFY_GO}, or install inotify-tools (apt-get install -y inotify-tools)"
    exit 1
fi
log "Using inotify backend: ${WATCHER_BIN}"

# Verify change script exists and is executable
if [ ! -x "$CHANGE_SCRIPT" ]; then
    log "ERROR: ${CHANGE_SCRIPT} not found or not executable"
    exit 1
fi

STATE_DIR="$(dirname "$STATE_FILE")"
STATE_BASENAME="$(basename "$STATE_FILE")"

log "Entering watch loop (watching directory: ${STATE_DIR})"

if "$WATCHER_BIN" -m -e moved_to,create,modify,close_write --format '%T %e %f' --timefmt '%Y-%m-%dT%H:%M:%S' "$STATE_DIR" |
while read -r timestamp event filename; do
    if [ "$filename" = "$STATE_BASENAME" ]; then
        log "State file changed: event=${event} file=${filename} at ${timestamp}"
        "$CHANGE_SCRIPT" &
    fi
done
then
    WATCHER_STATUS=0
else
    WATCHER_STATUS=$?
fi

# If the watch process exits unexpectedly
log "ERROR: ${WATCHER_BIN} exited unexpectedly (status=${WATCHER_STATUS})"
exit 1
