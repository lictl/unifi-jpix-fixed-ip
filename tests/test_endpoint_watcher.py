import copy
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "udm-jpix-watch"))
import reconcile


class EndpointWatcherTests(unittest.TestCase):
    def setUp(self):
        self.endpoint = reconcile.parse_pin("2001:db8:1::42/128")
        self.dhcp = {"type": "dynamic", "origin": "dhcp", "version": "v6"}
        self.items = [
            {"identification": {"id": "eth0"}, "addresses": []},
            {"identification": {"id": "eth9"}, "addresses": [self.dhcp],
             "ipv6": {"hb46pp": {"enabled": True, "capability": "ipip_jpix"}}},
            {"identification": {"id": "ip6tnl1"}, "tunnel": {"localAddress": {"id": "eth9"}}},
        ]
        self.live = copy.deepcopy(self.items)
        self.live[1]["ipv6"]["dhcp6PDStatus"] = [{"network": "2001:db8:1::/64"}]
        self.live[2]["tunnel"]["endpointStatus"] = {"localIPInUse": True, "localIP": str(self.endpoint.ip)}
        self.link = [{"linkinfo": {"info_kind": "ip6tnl", "info_data": {"local": str(self.endpoint.ip)}}}]
        self.address = {"family": "inet6", "prefixlen": 128, "local": str(self.endpoint.ip)}

        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        def replace(name, **kwargs):
            mock = patch.object(reconcile, name, **kwargs)
            self.addCleanup(mock.stop)
            return mock.start()
        self.run = Path(tmp.name)
        replace("RUN", new=self.run)
        replace("PAUSED", new=self.run / "paused")
        replace("get_udapi", return_value=self.live)
        replace("interfaces", side_effect=lambda: copy.deepcopy(self.items))
        replace("read_ip", side_effect=lambda *args: self.link if args[0] == "-d" else [{"addr_info": [self.address]}])
        self.put = replace("submit", side_effect=self.save)
        self.events = replace("emit")

    def save(self, items):
        self.items = copy.deepcopy(items)

    def watch(self):
        return reconcile.watch(copy.deepcopy(self.items), self.endpoint)

    def test_restore_then_check_without_another_write(self):
        expected = copy.deepcopy(self.items)
        expected[1]["addresses"].append({"type": "static", "version": "v6", "cidr": str(self.endpoint)})
        self.assertEqual(self.watch(), 0)
        self.assertEqual(self.items, expected)
        self.assertEqual(self.events.call_args.args[1], "repaired")
        self.assertEqual(self.watch(), 0)
        self.assertEqual(self.put.call_count, 1)
        self.assertEqual(self.events.call_args.args[1], "checked")

    def test_wait_for_kernel_readback(self):
        self.address["tentative"] = True
        self.assertEqual(self.watch(), 75)
        self.assertEqual(self.events.call_args.args[1], "awaiting-readback")

    def test_changed_prefix_and_changed_endpoint(self):
        self.live[1]["ipv6"]["dhcp6PDStatus"] = [{"network": "2001:db8:2::/64"}]
        with self.assertRaisesRegex(reconcile.GuardError, "delegated-prefix"):
            self.watch()
        self.link[0]["linkinfo"]["info_data"]["local"] = "2001:db8:2::42"
        self.assertEqual(self.watch(), 75)
        self.put.assert_not_called()

    def test_revert_then_pause(self):
        original = copy.deepcopy(self.items)
        self.assertEqual(self.watch(), 0)
        pin = self.run / "endpoint.conf"
        pin.write_text(str(self.endpoint))
        with patch.object(reconcile, "PIN", pin), patch.object(sys, "argv", ["reconcile.py", "revert"]):
            self.assertEqual(reconcile.main(), 0)
        self.assertEqual(self.items, original)
        self.assertEqual(self.watch(), 0)
        self.assertEqual(self.events.call_args.args[1], "paused")
        self.assertEqual(self.put.call_count, 2)

    def test_site_pin_requires_an_explicit_ipv6_host_prefix(self):
        for value in ("", "192.0.2.42/32", "2001:db8::42", "2001:db8::42/64", "::/128"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                reconcile.parse_pin(value)
