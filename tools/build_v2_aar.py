"""Bind the pinned libXray and Nexus V2 client into one Go runtime."""
import argparse
import json
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
    manifest = json.loads(subprocess.check_output(["go", "mod", "edit", "-json"], cwd=V2))
    checked = {QUIC[0], "github.com/refraction-networking/utls", "golang.org/x/net"}
    expected = {r["Path"]: r["Version"] for r in manifest["Require"] if r["Path"] in checked}
    if expected.keys() != checked:
        p.error("missing transport module pin in v2/go.mod")
    locked = set((V2 / "go.sum").read_text().splitlines())
    with zipfile.ZipFile(built) as aar:
        libraries = [n for n in aar.namelist() if n.startswith("jni/") and n.endswith("/libgojni.so")]
        if not libraries:
            p.error("missing native Go library")
        for name in libraries:
            binary = aar.extract(name, tmp)
            info = subprocess.check_output(["go", "version", "-m", binary], text=True)
            selected = {}
            previous = None
            for line in info.splitlines():
                fields = line.split()
                if previous in checked and fields[:1] == ["=>"]:
                    if previous != QUIC[0] or Path(fields[1]).resolve() != (a.patched / "aper").resolve():
                        p.error(f"{name}: unexpected replacement for {previous}")
                previous = fields[1] if len(fields) >= 3 and fields[0] == "dep" else None
                if previous in checked:
                    if previous != QUIC[0] and " ".join(fields[1:]) not in locked:
                        p.error(f"{name}: checksum differs from v2/go.sum for {previous}")
                    selected[fields[1]] = fields[2]
            if selected != expected:
                p.error(f"{name}: selected {selected}, expected {expected}")
    os.replace(built, output)
