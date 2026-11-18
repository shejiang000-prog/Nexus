"""Cycle an emulator's two networks while the connected Nexus VPN page is visible."""
import argparse
import subprocess
import time
import xml.etree.ElementTree as ET


def verify(serial):
    if not serial.startswith("emulator-"):
        raise ValueError("This test changes network state and requires an emulator")
    adb = ["adb", "-s", serial]

    def shell(*args):
        return subprocess.check_output(adb + ["shell", *args], text=True, encoding="utf-8", timeout=10)

    def wait_page(label):
        deadline = time.monotonic() + 35
        while time.monotonic() < deadline:
            dump = subprocess.check_output(adb + ["exec-out", "uiautomator", "dump", "/dev/tty"],
                                           text=True, encoding="utf-8", timeout=12)
            start, end = dump.find("<?xml"), dump.find("</hierarchy>")
            if start >= 0 and end > start:
                root = ET.fromstring(dump[start:end + len("</hierarchy>")])
                texts = [n.get("text") for n in root.iter("node")
                         if n.get("package") == "dev.nexus.localclient"]
                if label in texts:
                    return texts
            time.sleep(0.25)
        raise RuntimeError("Nexus did not show " + label)

    before = wait_page("VPN 已连接")
    address = next(s.rsplit(":", 1)[0] for s in before if s and s.count(":") == 1)
    def check_route():
        routes = shell("ip", "-o", "route", "show", "table", "all").splitlines()
        assert any(line.startswith(address + " dev tun") for line in routes), "VPN route missing"
    check_route()
    for setting in ("wifi_on", "mobile_data"):
        if shell("settings", "get", "global", setting).strip() != "1":
            raise ValueError("Start this test with both Wi-Fi and mobile data enabled")
    try:
        shell("svc", "data", "disable")
        shell("svc", "wifi", "disable")
        wait_page("等待网络")
        check_route()
        print("Offline: waiting state reached, private host route retained", flush=True)
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
    parser.add_argument("--serial", required=True)
    verify(parser.parse_args().serial)
