"""Build the Windows x64 sidecar alongside the Tauri executable."""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import zipfile

from generate_split_rules import generate
from prepare_v2 import ROOT, patch, prepare, stage_client

WINTUN_ARCHIVE = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
WINTUN_AMD64 = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"


def package_wintun(archive, dest):
    with archive.open("rb") as source:
        if hashlib.file_digest(source, "sha256").hexdigest() != WINTUN_ARCHIVE:
            raise ValueError("unexpected Wintun archive")
        with zipfile.ZipFile(source) as upstream:
            dll = upstream.read("wintun/bin/amd64/wintun.dll")
            if hashlib.sha256(dll).hexdigest() != WINTUN_AMD64:
                raise ValueError("unexpected Wintun AMD64 DLL")
            (dest / "wintun.dll").write_bytes(dll)
            (dest / "Wintun-LICENSE.txt").write_bytes(upstream.read("wintun/LICENSE.txt"))
    shutil.copyfile(ROOT / "assets/licenses/wintun-go/LICENSE", dest / "Wintun-Go-LICENSE.txt")
    shutil.copyfile(ROOT / "assets/licenses/wireguard-windows/COPYING", dest / "WireGuard-Windows-COPYING.txt")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="local pinned libXray checkout")
    parser.add_argument("output", type=Path, help="destination nexus-core.exe")
    parser.add_argument("--rules-cache", type=Path, default=os.environ.get("NEXUS_SPLIT_CACHE",
                        ROOT / "apps/android/app/build/splitDownloads"), help="Android routing data download cache")
    parser.add_argument("--test-routing", action="store_true", help="check split/global parity on a Windows x64 host")
    parser.add_argument("--test-subscriptions", action="store_true", help="check synthetic subscriptions and Windows storage")
    parser.add_argument("--test-tun", action="store_true", help="run isolated TUN checks without UAC or network changes")
    parser.add_argument("--wintun-zip", type=Path, help="package the verified official Wintun 0.14.1 AMD64 DLL and licenses")
    args = parser.parse_args()
    output = args.output.resolve()
    if output.name.lower() != "nexus-core.exe":
        parser.error("output must be named nexus-core.exe")
    if subprocess.check_output(["go", "env", "GOVERSION"], text=True).strip() != "go1.27.4":
        parser.error("Go 1.27.4 is required")
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as scratch, tempfile.TemporaryDirectory(dir=output.parent) as publish:
        work = Path(scratch).resolve()
        patched = work / "patched"
        prepare(patched, android=True)
        patch(patched / "xray", "xray-desktop-socks-udp", "xray-desktop-packet-tun", "xray-desktop-dns-guard")
        lib = stage_client(work, args.source, patched)
        # Staged Go sources can come from a CRLF Windows working copy.
        for rel in ("internal/nexusv2/transport/tls.go", "internal/nexusv2/transport/client.go", "nexusmobile/mobile.go"):
            src = lib / rel
            src.write_bytes(src.read_bytes().replace(b"\r\n", b"\n"))
        patch(lib, "desktop-v2-egress")
        shutil.copytree(ROOT / "apps/shared/subscriptions", lib / "subscriptions")
        shutil.copytree(ROOT / "apps/desktop/tuntrust", lib / "tuntrust")
        shutil.copytree(ROOT / "apps/desktop/tunhelper", lib / "cmd/nexus-tun-helper")
        # Compile only the pinned WFP bindings and DNS guard into the elevated helper.
        guard = lib / "cmd/nexus-tun-helper/dnsguard"
        guard.mkdir()
        for name in ("tun_windows_wfp.go", "wfp_leak.go", "desktop_dns_guard_windows.go", "desktop_dns_guard_windows_test.go"):
            shutil.copyfile(patched / "xray/proxy/tun" / name, guard / name)
        shutil.copytree(ROOT / "apps/desktop/core", lib / "cmd/nexus-core")
        generate(args.rules_cache, lib / "cmd/nexus-core/split-rules.txt")
        env = dict(os.environ, GOOS="windows", GOARCH="amd64", CGO_ENABLED="0", GOWORK="off")
        binary = Path(publish) / output.name
        helper = Path(publish) / "nexus-tun-helper.exe"
        if args.test_routing:
            subprocess.run(["go", "test", "-mod=readonly", "-ldflags=-checklinkname=0",
                            "-run", "^TestDesktopRouting$", "./cmd/nexus-core"], cwd=lib, env=env, check=True)
        if args.test_subscriptions:
            subprocess.run(["go", "test", "-mod=readonly", "-ldflags=-checklinkname=0",
                            "-run", "Subscription", "./subscriptions", "./cmd/nexus-core"], cwd=lib, env=env, check=True)
        if args.test_tun:
            subprocess.run(["go", "test", "-mod=readonly", "-ldflags=-checklinkname=0", "-run", "Tun",
                            "./cmd/nexus-core"], cwd=lib, env=env, check=True)
            subprocess.run(["go", "test", "-mod=readonly", "./cmd/nexus-tun-helper/..."], cwd=lib, env=env, check=True)
        subprocess.run(["go", "build", "-mod=readonly", "-trimpath", "-ldflags=-s -w", "-o", helper,
                        "./cmd/nexus-tun-helper"], cwd=lib, env=env, check=True)
        with helper.open("rb") as f:
            helper_hash = hashlib.file_digest(f, "sha256").hexdigest()
        subprocess.run(["go", "build", "-mod=readonly", "-trimpath",
                        "-ldflags=-checklinkname=0 -s -w -X main.tunHelperSHA256=" + helper_hash,
                        "-o", binary, "./cmd/nexus-core"], cwd=lib, env=env, check=True)
        if args.wintun_zip:
            package_wintun(args.wintun_zip, Path(publish))
        for extra in Path(publish).iterdir():
            if extra != binary:
                os.replace(extra, output.parent / extra.name)
        os.replace(binary, output)
