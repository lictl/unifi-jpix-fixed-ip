# JPIX tunnel endpoint watcher

Use this optional watcher if **native JPIX v6 Plus Fixed IP (IPIP)** is already working but the gateway periodically removes the tunnel's local IPv6 address from the WAN. On a UDM-SE running UniFi OS 6.0.17 and Network 11.0.99, that removal briefly interrupted IPv4 traffic. Adding the same address as a static IPv6 `/128` alongside the existing DHCPv6 entry stopped the observed removals over the next three prefix refreshes. Saving UniFi Network settings later erased the entry, and the gateway watcher restored it and verified it on the WAN.

This version supports the observed layout, WAN `eth9` and tunnel `ip6tnl1`. It requires root access, systemd, Python 3.9+, iproute2 with JSON output, and `ubios-udapi-client`. UniFi must already have a working native `ipip_jpix` configuration with DHCPv6 prefix delegation. Install only this package for that setup. The repository's older `network_watcher` configures a different WAN mode.

## Install

Extract `udm-jpix-watch.tar.gz` or use the `udm-jpix-watch` directory from a checkout. Copy the directory to the gateway, replacing `unifi` with its hostname.

```sh
scp -r udm-jpix-watch root@unifi:/tmp/
ssh root@unifi
cd /tmp/udm-jpix-watch
ip -6 tunnel show ip6tnl1
```

Read the IPv6 address after **local**. Append `/128` and use your own value below. The example is a documentation address. The first command checks the live tunnel, DHCPv6 and delegated prefix using read-only network queries. Install when the check succeeds and you want to enable automatic repair.

```sh
endpoint='2001:db8:1234:5678::42/128'
python3 -IB reconcile.py check "$endpoint"
./install.sh "$endpoint"
```

The installer pins the address in `/data/scripts/udm-jpix-watch/endpoint.conf`. It enables a boot check and watches `/data/udapi-config/ubios-udapi-server/ubios-udapi-server.state` with a systemd path unit. After a trigger, the service waits five seconds for provisioning to settle. A Mac, scheduled poll, and ISP credentials are unnecessary.

## What the watcher writes

The intended change is one additional member of the WAN's `addresses` array. With the example endpoint, that member is:

```json
{"type":"static","version":"v6","cidr":"2001:db8:1234:5678::42/128"}
```

The watcher reads desired interfaces from UDAPI `GET /system/configuration`, identifies the WAN by ID, strips runtime-only status fields, and appends the pinned address when missing. It checks that UDAPI and the kernel agree on the active tunnel endpoint and that the address belongs to the current delegated prefix. It rereads the desired interfaces immediately before `PUT /interfaces`, which takes the complete writable interface array. DHCPv6, prefix delegation and DAD retain their existing settings. The state file only triggers checks. Repair goes through UDAPI.

A `repaired` journal entry means the watcher read back the managed entry and found its usable `/128` on the kernel WAN interface. It does not measure IPv4 packet loss. A change of endpoint or prefix that fails validation defers the write. Failed checks retry after one minute, with at least five minutes between automatic write attempts.

## Check, pause and roll back

Run these commands on the gateway. The path unit should be `active (waiting)`. The one-shot service normally exits after a successful check.

```sh
cd /data/scripts/udm-jpix-watch
python3 -IB reconcile.py check
systemctl status udm-jpix-watch.path
journalctl -u udm-jpix-watch.service -n 30 --no-pager
```

To pause future repairs while keeping the current address, run `touch /data/scripts/udm-jpix-watch/auto-repair.paused`. To resume, run `python3 -IB reconcile.py apply` from the installed directory. It checks the pinned target, restores the entry if needed and clears the pause flag.

To **remove the workaround**, run `python3 -IB reconcile.py revert` from the installed directory. This pauses automatic repair first and removes the matching static entry even if it predated installation. Use it only if you intend to remove that entry. The original IPv4 interruptions may return.

To uninstall the watcher, optionally revert the address first, then run:

```sh
systemctl disable --now udm-jpix-watch.path
systemctl disable --now udm-jpix-watch.service
rm -f /etc/systemd/system/udm-jpix-watch.path /etc/systemd/system/udm-jpix-watch.service
systemctl daemon-reload
```

This leaves the pin and script under `/data/scripts/udm-jpix-watch`. If an OS upgrade removes the units from `/etc`, copy the package back to `/tmp` and run `./install.sh` there. It reuses the saved pin, checks it against the live configuration, and respects the pause flag.
