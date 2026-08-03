#!/bin/sh

# Configure JPIX "v6 Plus" Fixed IP service (IPIP).
# Intended for unattended use (e.g., crontab).
#
# Prerequisites: System must already have MAP-E JPIX configured.

set -eu
umask 077

# Configuration file. Script becomes a no-op when this file is missing.
CONFIG_FILE="/data/scripts/ipip/config.env"

# ── Parameters (loaded from config file) ──────────────────────────────
# Required keys in ${CONFIG_FILE}:
#   USERNAME, PASSWORD, IPV4_CIDR, IPV6_IID, IPV6_REMOTE

STATE_FILE="/data/udapi-config/ubios-udapi-server/ubios-udapi-server.state"
WORKING_FILE="${STATE_FILE}.modified"
TEMP_FILE="${WORKING_FILE}.tmp"

# ──────────────────────────────────────────────────────────────────────

DRY_RUN=false
case "${1:-}" in --dry-run) DRY_RUN=true ;; esac

if [ ! -f "$CONFIG_FILE" ]; then
  echo "Config file not found at ${CONFIG_FILE}; skipping (no-op)."
  exit 0
fi

# shellcheck disable=SC1090
. "$CONFIG_FILE"

# Validate required config keys to avoid partially applying invalid config.
: "${USERNAME:?Missing USERNAME in ${CONFIG_FILE}}"
: "${PASSWORD:?Missing PASSWORD in ${CONFIG_FILE}}"
: "${IPV4_CIDR:?Missing IPV4_CIDR in ${CONFIG_FILE}}"
: "${IPV6_IID:?Missing IPV6_IID in ${CONFIG_FILE}}"
: "${IPV6_REMOTE:?Missing IPV6_REMOTE in ${CONFIG_FILE}}"

validate_ipv4_cidr() {
  value=$1

  case "$value" in
    */*) ;;
    *) return 1 ;;
  esac

  address=${value%/*}
  prefix=${value##*/}

  case "$address" in
    ""|.*|*.|*..*|*[!0-9.]*) return 1 ;;
  esac
  case "$prefix" in
    ""|*[!0-9]*|*/*|???*) return 1 ;;
  esac
  [ "$prefix" -le 32 ] || return 1

  old_ifs=$IFS
  IFS=.
  # Intentional field splitting: each dot-delimited component becomes one
  # positional parameter for the octet count and range checks below.
  # shellcheck disable=SC2086
  set -- $address
  IFS=$old_ifs

  [ "$#" -eq 4 ] || return 1
  for octet do
    case "$octet" in
      ""|*[!0-9]*|????*) return 1 ;;
    esac
    [ "$octet" -le 255 ] || return 1
  done
}

if ! validate_ipv4_cidr "$IPV4_CIDR"; then
  echo "Invalid IPV4_CIDR in ${CONFIG_FILE}: ${IPV4_CIDR}" >&2
  echo "Expected an IPv4 CIDR such as 1.1.1.1/32." >&2
  exit 1
fi

# Split CIDR into address and prefix length.
IPV4_ADDR="${IPV4_CIDR%/*}"
IPV4_PREFIX="${IPV4_CIDR#*/}"

cleanup() {
  rm -f "$WORKING_FILE" "$TEMP_FILE" || true
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

# Create a root-only working copy because the generated JSON contains the
# Fixed IP credentials.
cp "$STATE_FILE" "$WORKING_FILE"
chmod 600 "$WORKING_FILE"

# Build the serverName payload: IID|IPv6_Remote|IPv4_Address|IPv4_Prefix
SERVER_NAME="${IPV6_IID}|${IPV6_REMOTE}|${IPV4_ADDR}|${IPV4_PREFIX}"

# Apply modifications with jq:
#   1. Change capability from map_e_jpix → ipip_jpix
#   2. Add authentication block with serverName encoding
#   3. Remove 192.0.0.x addresses from all interfaces
jq --indent 1 \
  --arg user "$USERNAME" \
  --arg pass "$PASSWORD" \
  --arg sname "$SERVER_NAME" \
'
  .interfaces |= [
    .[] |
    # Update hb46pp capability and add authentication
    if .ipv6.hb46pp.capability == "map_e_jpix" then
      .ipv6.hb46pp.capability = "ipip_jpix"
      | .ipv6.hb46pp.authentication = {
          username: $user,
          password: $pass,
          serverName: $sname
        }
    else . end
    |
    # Remove 192.0.0.x addresses
    if .addresses then
      .addresses |= [ .[] | select(.cidr == null or (.cidr | startswith("192.0.0.") | not)) ]
    else . end
  ]
' "$WORKING_FILE" > "$TEMP_FILE" && mv "$TEMP_FILE" "$WORKING_FILE"

# Dry run: show diff and exit without applying.
if [ "$DRY_RUN" = true ]; then
  diff -u "$STATE_FILE" "$WORKING_FILE" || true
  exit 0
fi

# Apply changes.
ubios-udapi-client put /system/ubios/udm/configuration "@${WORKING_FILE}"
