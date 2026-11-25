"""Build the Windows x64 sidecar alongside the Tauri executable."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

from prepare_v2 import ROOT, prepare, stage_client

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="local pinned libXray checkout")
    parser.add_argument("output", type=Path, help="destination nexus-core.exe")
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
        lib = stage_client(work, args.source, patched)
        shutil.copytree(ROOT / "apps/desktop/core", lib / "cmd/nexus-core")
        env = dict(os.environ, GOOS="windows", GOARCH="amd64", CGO_ENABLED="0", GOWORK="off")
        binary = Path(publish) / output.name
        subprocess.run(["go", "build", "-mod=readonly", "-trimpath", "-ldflags=-checklinkname=0 -s -w",
                        "-o", binary, "./cmd/nexus-core"], cwd=lib, env=env, check=True)
        os.replace(binary, output)
