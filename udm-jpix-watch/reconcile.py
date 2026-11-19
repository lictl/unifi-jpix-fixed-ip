import copy
import fcntl
import ipaddress
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time


WAN = "eth9"
TUNNEL = "ip6tnl1"
PIN = Path(__file__).with_name("endpoint.conf")
UDAPI = "/usr/bin/ubios-udapi-client"
PAUSED = Path("/data/scripts/udm-jpix-watch/auto-repair.paused")
RUN = Path("/run/udm-jpix-repair")
COOLDOWN = 300
READ_ONLY_STATUS = ("currentSpeed", "currentFlowControl", "currentEnabled", "plugged", "currentMTU")


class GuardError(Exception):
    """A change in site configuration or readiness prevents a safe write."""


def get_udapi(path):
    return json.loads(subprocess.check_output([UDAPI, "-r", "GET", path], timeout=8,
                                              stderr=subprocess.DEVNULL))


def interfaces():
    items = get_udapi("/system/configuration")["interfaces"]
    for item in items:
        for key in READ_ONLY_STATUS:
            item.get("status", {}).pop(key, None)
    return items


def wan(items):
    result, = [item for item in items if item["identification"]["id"] == WAN]
    return result


def parse_pin(value):
    address = ipaddress.IPv6Interface(value)
    if not value.endswith("/128") or address.ip.is_unspecified or address.ip.is_multicast or address.scope_id:
        raise ValueError("Expected an IPv6 tunnel endpoint with an explicit /128")
    return address


def matches(entry, expected):
    return (entry.get("type") == "static" and entry.get("version") == "v6"
            and entry.get("cidr") and ipaddress.IPv6Interface(entry["cidr"]) == expected)


def read_ip(*args):
    result = subprocess.run(["/usr/sbin/ip", "-j", *args], capture_output=True, timeout=5)
    if result.returncode:
        return []
    return json.loads(result.stdout)


def evaluate(config, links, wan_addrs, expected):
    interfaces = {item["identification"]["id"]: item for item in config["interfaces"]}
    wan = interfaces[WAN]
    addresses = wan["addresses"]
    if not isinstance(addresses, list):
        raise ValueError("address list unavailable")
    static = {ipaddress.IPv6Interface(a["cidr"]) for a in addresses
              if a.get("type") == "static" and a.get("version") == "v6" and a.get("cidr")}
    provider = wan.get("ipv6", {}).get("hb46pp") or {}
    tunnel = interfaces.get(TUNNEL, {}).get("tunnel") or {}
    info = links[0].get("linkinfo", {}) if links else {}
    local = info.get("info_data", {}).get("local")
    jpix = bool(provider.get("enabled") and provider.get("capability") == "ipip_jpix")
    on_wan = (tunnel.get("localAddress") or {}).get("id") == WAN
    endpoint = None
    if jpix and on_wan and info.get("info_kind") == "ip6tnl" and local:
        ip = ipaddress.IPv6Address(local)
        if not ip.is_unspecified:
            endpoint = ipaddress.IPv6Interface((ip, 128))
    tracked = expected in static
    details = {"tracked_static": tracked, "jpix": jpix, "wan": WAN}
    if endpoint is None:
        if not tracked:
            return dict(details, status="MISSING", reason="tested-entry-absent-endpoint-unavailable")
        return dict(details, status="UNKNOWN", reason="active-jpix-endpoint-unavailable")
    details["endpoint"] = "tested" if endpoint == expected else "changed"
    if endpoint != expected:
        return dict(details, status="UNKNOWN", reason="active-tunnel-endpoint-changed")
    if endpoint not in static:
        return dict(details, status="MISSING", reason="active-endpoint-has-no-managed-/128")
    ready = any(a.get("family") == "inet6" and a.get("prefixlen") == 128
                and ipaddress.IPv6Address(a["local"]) == endpoint.ip
                and not a.get("tentative") and not a.get("dadfailed")
                for interface in wan_addrs for a in interface["addr_info"])
    if not ready:
        return dict(details, status="UNKNOWN", reason="managed-entry-present-kernel-address-unready")
    return dict(details, status="PRESENT", reason="active-endpoint-managed-and-assigned")


def inspect(items, expected):
    return evaluate({"interfaces": items}, read_ip("-d", "link", "show", "dev", TUNNEL),
                    read_ip("-6", "address", "show", "dev", WAN), expected)


def verify_target(items, expected):
    w = wan(items)
    provider = w.get("ipv6", {}).get("hb46pp") or {}
    if not provider.get("enabled") or provider.get("capability") != "ipip_jpix":
        raise GuardError("jpix-ipip-disabled")
    if not any(a.get("type") == "dynamic" and a.get("version") == "v6" and a.get("origin") == "dhcp"
               for a in w["addresses"]):
        raise GuardError("wan-dhcpv6-changed")
    live = {i["identification"]["id"]: i for i in get_udapi("/interfaces")}
    tunnel = live.get(TUNNEL, {}).get("tunnel") or {}
    ep = tunnel.get("endpointStatus") or {}
    if ((tunnel.get("localAddress") or {}).get("id") != WAN or not ep.get("localIPInUse")
            or not ep.get("localIP") or ipaddress.IPv6Address(ep["localIP"]) != expected.ip):
        raise GuardError("active-tunnel-endpoint-changed-or-unavailable")
    links = read_ip("-d", "link", "show", "dev", TUNNEL)
    info = links[0].get("linkinfo", {}) if links else {}
    local = info.get("info_data", {}).get("local")
    if info.get("info_kind") != "ip6tnl" or not local or ipaddress.IPv6Address(local) != expected.ip:
        raise GuardError("kernel-endpoint-changed-or-unavailable")
    prefixes = live[WAN]["ipv6"].get("dhcp6PDStatus") or []
    if not any(expected.ip in ipaddress.ip_network(p["network"]) for p in prefixes):
        raise GuardError("delegated-prefix-changed-or-unavailable")


def submit(items):
    with tempfile.NamedTemporaryFile(mode="w") as payload:
        json.dump(items, payload)
        payload.flush()
        r = subprocess.run([UDAPI, "-r", "PUT", "/interfaces", "@" + payload.name],
                           capture_output=True, timeout=20, check=True)
    if r.stdout.strip():
        body = json.loads(r.stdout)
        if isinstance(body, dict) and (body.get("error") or int(body.get("statusCode", 200)) >= 400):
            raise GuardError("udapi-rejected-update")


def change(before, expected, *, add, automatic=False):
    desired = copy.deepcopy(before)
    addresses = wan(desired)["addresses"]
    entries = [a for a in addresses if matches(a, expected)]
    if len(entries) > 1:
        raise GuardError("duplicate-managed-entries")
    if add:
        verify_target(before, expected)
    if bool(entries) == add:
        return False
    if add:
        if any(a.get("cidr") and ipaddress.ip_interface(a["cidr"]).ip == expected.ip for a in addresses):
            raise GuardError("conflicting-configured-address")
        addresses.append({"type": "static", "version": "v6", "cidr": str(expected)})
    else:
        addresses.remove(entries[0])
    stamp = RUN / "last-attempt"
    if automatic and stamp.exists() and time.monotonic() - float(stamp.read_text()) < COOLDOWN:
        raise GuardError("repair-cooldown")
    if add:
        verify_target(before, expected)
    if interfaces() != before:
        raise GuardError("configuration-changed-during-read")
    if automatic:
        stamp.write_text(str(time.monotonic()))
    submit(desired)
    if any(matches(a, expected) for a in wan(interfaces())["addresses"]) != add:
        raise GuardError("write-not-confirmed")
    return True


def emit(result, action):
    result = dict(result, action=action, trigger=os.environ.get("TRIGGER_UNIT") or "service-start")
    print("JPIX " + json.dumps(result, separators=(",", ":")), flush=True)


def watch(before, expected):
    status = inspect(before, expected)
    if PAUSED.exists():
        emit(status, "paused")
        return 0
    if status["status"] != "MISSING":
        emit(status, "checked")
        return 75 if status["status"] == "UNKNOWN" else 0
    emit(status, "detected")
    change(before, expected, add=True, automatic=True)
    after = inspect(interfaces(), expected)
    emit(after, "repaired" if after["status"] == "PRESENT" else "awaiting-readback")
    return 0 if after["status"] == "PRESENT" else 75


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "check"
    if mode not in ("watch", "check", "apply", "revert"):
        sys.exit("Expected watch, check, apply or revert")
    if len(sys.argv) > 2 and (mode != "check" or len(sys.argv) != 3):
        sys.exit("Only check accepts an optional IPv6/128 argument")
    os.umask(0o077)
    try:
        expected = parse_pin(sys.argv[2] if len(sys.argv) == 3 else PIN.read_text().strip())
        RUN.mkdir(mode=0o700, exist_ok=True)
        with (RUN / "lock").open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            if mode == "revert":
                PAUSED.touch()
            before = interfaces()
            if mode == "watch":
                return watch(before, expected)
            if mode == "check":
                print("Managed entry on eth9:", "present" if any(matches(a, expected) for a in wan(before)["addresses"]) else "absent")
                print("Repair pause flag:", "set" if PAUSED.exists() else "clear")
                verify_target(before, expected)
                print("The active JPIX endpoint and delegated prefix match the tested entry.")
                return 0
            changed = change(before, expected, add=mode == "apply")
            if mode == "apply":
                PAUSED.unlink(missing_ok=True)
            print(mode, "verified;", "updated" if changed else "already in requested state;",
                  "automatic repair", "paused" if PAUSED.exists() else "enabled")
            return 0
    except (GuardError, OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        reason = str(error) if isinstance(error, GuardError) else type(error).__name__
        if mode == "watch":
            emit({"status": "UNKNOWN", "reason": reason, "wan": WAN}, "deferred")
        else:
            print("JPIX:", reason, file=sys.stderr)
        return 75


if __name__ == "__main__":
    sys.exit(main())
