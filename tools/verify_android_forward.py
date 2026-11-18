"""Verify an already connected Nexus emulator against tools/android_lab.py, then tap Disconnect."""
import argparse
import hashlib
import http.client
import os
import re
import subprocess
import time
import xml.etree.ElementTree as ET


def verify(serial):
    adb = ["adb", "-s", serial]

    def shell(*args):
        return subprocess.check_output(adb + ["shell", *args], text=True, encoding="utf-8")

    def nodes():
        dump = subprocess.check_output(adb + ["exec-out", "uiautomator", "dump", "/dev/tty"],
                                       text=True, encoding="utf-8")
        xml = dump[dump.index("<?xml"):dump.index("</hierarchy>") + len("</hierarchy>")]
        return [n for n in ET.fromstring(xml).iter("node")
                if n.get("package") == "dev.nexus.localclient"]

    active = nodes()
    port = next(int(n.get("text").split(":")[1]) for n in active
                if re.fullmatch(r"127\.0\.0\.1:\d+", n.get("text", "")))
    button = next(n for n in active if n.get("text") == "断开")
    listener = f"127.0.0.1:{port}"
    assert listener in shell("ss", "-ltn").split(), "Android listener missing"
    local = subprocess.check_output(adb + ["forward", "tcp:0", f"tcp:{port}"], text=True).strip()
    try:
        payload = os.urandom(512 * 1024)
        conn = http.client.HTTPConnection("127.0.0.1", int(local), timeout=20)
        try:
            conn.request("POST", "/", payload)
            response = conn.getresponse()
            data = response.read(len(payload) + 1)
            assert response.status == 200 and data == payload, "HTTP echo mismatch"
            print(f"HTTP 200, sent={len(payload)}, received={len(data)}, SHA256={hashlib.sha256(data).hexdigest()}")
        finally:
            conn.close()

        x1, y1, x2, y2 = map(int, re.findall(r"\d+", button.get("bounds")))
        shell("input", "tap", str((x1 + x2) // 2), str((y1 + y2) // 2))
        deadline = time.monotonic() + 5
        while listener in shell("ss", "-ltn").split():
            if time.monotonic() >= deadline:
                raise RuntimeError("Android listener survived disconnect")
            time.sleep(0.1)
        assert any(n.get("text") == "未连接" for n in nodes()), "UI did not disconnect"
        probe = subprocess.run(adb + ["shell", "toybox", "nc", "-w", "2", "127.0.0.1", str(port)],
                               input="", text=True, capture_output=True, timeout=5)
        assert probe.returncode != 0 and "refused" in (probe.stdout + probe.stderr).lower(), probe
        print(f"Android {listener} absent from LISTEN; direct TCP connection refused")
    finally:
        subprocess.run(adb + ["forward", "--remove", f"tcp:{local}"], check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--serial", required=True)
    verify(parser.parse_args().serial)
