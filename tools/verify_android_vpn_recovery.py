"""Cycle an emulator's two networks while the connected Nexus VPN page is visible."""
import argparse
import locale
import shlex
import subprocess
import time
import xml.etree.ElementTree as ET


def verify(serial, ld_shell=None, ld_index=0):
    if ld_shell is None and not serial.startswith("emulator-"):
        raise ValueError("This test changes network state and requires an emulator")
    def shell(*args):
        if ld_shell:
            # LDPlayer's ADB connection uses guest Wi-Fi. Its native console remains available offline.
            cmd = [ld_shell, "-s", str(ld_index), shlex.join(args)]
            encoding = locale.getencoding()
        else:
            cmd = ["adb", "-s", serial, "shell", *args]
            encoding = "utf-8"
        return subprocess.check_output(cmd, text=True, encoding=encoding, errors="replace", timeout=12)

    def wait_page(*labels, seconds=35):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            dump = shell("uiautomator", "dump", "/proc/self/fd/1")
            start, end = dump.find("<?xml"), dump.find("</hierarchy>")
            if start >= 0 and end > start:
                root = ET.fromstring(dump[start:end + len("</hierarchy>")])
                texts = [n.get("text") for n in root.iter("node")
                         if n.get("package") == "dev.nexus.localclient"]
                if any(label in texts for label in labels):
                    return texts
            time.sleep(0.25)
        raise RuntimeError("Nexus did not show " + " / ".join(labels))

    before = wait_page("VPN 已连接")
    full_device = any(s and s.startswith("全设备 VPN") for s in before)
    address = "default" if full_device else next(s.rsplit(":", 1)[0] for s in before if s and s.count(":") == 1)
    def check_route():
        routes = shell("ip", "-o", "route", "show", "table", "all").splitlines()
        assert any(line.startswith(address + " dev tun") for line in routes), "VPN route missing"
        if full_device:
            ipv6 = shell("ip", "-6", "route", "show", "table", "all").splitlines()
            assert any(line.startswith("default dev tun") for line in ipv6), "IPv6 VPN route missing"
    check_route()
    for setting in ("wifi_on", "mobile_data"):
        if shell("settings", "get", "global", setting).strip() != "1":
            raise ValueError("Start this test with both Wi-Fi and mobile data enabled")
    try:
        shell("svc", "data", "disable")
        shell("svc", "wifi", "disable")
        if ld_shell:
            # LDPlayer may retain a Wi-Fi NetworkAgent after its guest interface goes down.
            # Allow the authenticated node health check to detect loss in that case.
            wait_page("等待网络", "节点不可用", seconds=75)
        else:
            wait_page("等待网络")
        check_route()
        print("Offline: waiting state reached, VPN routes retained", flush=True)
    finally:
        try:
            shell("svc", "wifi", "enable")
        finally:
            shell("svc", "data", "enable")
    wait_page("VPN 已连接")
    check_route()
    print("Recovered: private node authenticated and VPN ready")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    target = parser.add_mutually_exclusive_group(required=True)
    target.add_argument("--serial")
    target.add_argument("--ld-shell", help="Path to LDPlayer's ld.exe")
    parser.add_argument("--ld-index", type=int, default=0)
    args = parser.parse_args()
    verify(args.serial, args.ld_shell, args.ld_index)
