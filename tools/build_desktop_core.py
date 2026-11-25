"""Build the Windows x64 sidecar alongside the Tauri executable."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

from generate_split_rules import generate
from prepare_v2 import ROOT, patch, prepare, stage_client

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="local pinned libXray checkout")
    parser.add_argument("output", type=Path, help="destination nexus-core.exe")
    parser.add_argument("--rules-cache", type=Path, default=os.environ.get("NEXUS_SPLIT_CACHE",
                        ROOT / "apps/android/app/build/splitDownloads"), help="Android routing data download cache")
    parser.add_argument("--test-routing", action="store_true", help="check split/global parity on a Windows x64 host")
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
        patch(patched / "xray", "xray-desktop-socks-udp")
        lib = stage_client(work, args.source, patched)
        shutil.copytree(ROOT / "apps/desktop/core", lib / "cmd/nexus-core")
        generate(args.rules_cache, lib / "cmd/nexus-core/split-rules.txt")
        env = dict(os.environ, GOOS="windows", GOARCH="amd64", CGO_ENABLED="0", GOWORK="off")
        binary = Path(publish) / output.name
        if args.test_routing:
            subprocess.run(["go", "test", "-mod=readonly", "-ldflags=-checklinkname=0",
                            "-run", "^TestDesktopRouting$", "./cmd/nexus-core"], cwd=lib, env=env, check=True)
        subprocess.run(["go", "build", "-mod=readonly", "-trimpath", "-ldflags=-checklinkname=0 -s -w",
                        "-o", binary, "./cmd/nexus-core"], cwd=lib, env=env, check=True)
        os.replace(binary, output)
