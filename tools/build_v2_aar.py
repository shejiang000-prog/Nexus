"""Bind the pinned libXray and Nexus V2 client into one Go runtime."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import zipfile
from prepare_v2 import ROOT, V2, QUIC, XRAY

LIB = "5a0f7d8d1c28b34afb5727b04b26a10f6dbac7e3"
MOBILE = "v0.0.0-20261011034528-197770444867"
p = argparse.ArgumentParser(description=__doc__)
p.add_argument("source", type=Path, help="local libXray git checkout")
p.add_argument("patched", type=Path, help="output of prepare_v2.py --android")
p.add_argument("output", type=Path)
p.add_argument("--target", default="android")
a = p.parse_args()
output = a.output.resolve()
if output.is_relative_to(ROOT) or output.exists():
    p.error("choose a new output AAR outside the checkout")
for tool in ("gomobile", "gobind"):
    info = subprocess.check_output(["go", "version", "-m", shutil.which(tool)], text=True)
    if not any(line.split()[:3] == ["mod", "golang.org/x/mobile", MOBILE] for line in info.splitlines()):
        p.error("unexpected " + tool + " version")
with tempfile.TemporaryDirectory(dir=output.parent) as tmp:
    lib = Path(tmp) / "lib"
    archive = Path(tmp) / "source.zip"
    subprocess.run(["git", "archive", "--format=zip", "-o", archive, LIB], cwd=a.source, check=True)
    with zipfile.ZipFile(archive) as z:
        z.extractall(lib)
    for package in ("internal/congestion", "udp", "transport", "proof", "mobile"):
        dest = lib / ("nexusmobile" if package == "mobile" else "internal/nexusv2/" + package)
        shutil.copytree(V2 / package, dest, ignore=shutil.ignore_patterns("*_test.go", "testdata"))
        for f in dest.rglob("*.go"):
            f.write_bytes(f.read_bytes().replace(b"github.com/shejiang000-prog/Nexus/v2/", b"github.com/xtls/libxray/internal/nexusv2/"))
    for flag in ["-require=" + "@".join(QUIC),
                 "-replace=" + QUIC[0] + "=" + (a.patched.resolve()/"aper").as_posix(),
                 "-replace=" + XRAY[0] + "=" + (a.patched.resolve()/"xray").as_posix()]:
        subprocess.run(["go", "mod", "edit", flag], cwd=lib, check=True)
    built = Path(tmp) / "combined.aar"
    subprocess.run(["gomobile", "bind", "-target", a.target, "-androidapi", "21",
                    "-trimpath", "-ldflags=-checklinkname=0 -s -w -buildid= -extldflags=-Wl,-z,max-page-size=16384",
                    "-o", built, ".", "./nexusmobile"], cwd=lib, check=True)
    os.replace(built, output)
