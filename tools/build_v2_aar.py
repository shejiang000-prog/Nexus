"""Bind the pinned libXray and Nexus V2 client into one Go runtime."""
import argparse
from contextlib import contextmanager
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import zipfile
from prepare_v2 import ROOT, V2, QUIC, XRAY, prepare

LIB = "5a0f7d8d1c28b34afb5727b04b26a10f6dbac7e3"
MOBILE = "v0.0.0-20261011034528-197770444867"
p = argparse.ArgumentParser(description=__doc__)
p.add_argument("source", type=Path, help="local libXray git checkout")
p.add_argument("patched", type=Path, nargs="?", help="output of prepare_v2.py --android (default: prepare fresh verified sources)")
p.add_argument("output", type=Path)
a = p.parse_args()
output = a.output.resolve()
if output.is_relative_to(ROOT) and output != ROOT / "apps/android/app/build/engines/nexus-v2.aar":
    p.error("output must be the Gradle build artifact or an external AAR")
output.parent.mkdir(parents=True, exist_ok=True)
if subprocess.check_output(["go", "env", "GOVERSION"], text=True).strip() != "go1.27.4":
    p.error("Go 1.27.4 is required")
if not a.source.is_dir():
    p.error("set NEXUS_LIBXRAY_SOURCE to the local pinned libXray checkout")
for tool in ("gomobile", "gobind"):
    exe = shutil.which(tool)
    if not exe:
        p.error("missing pinned " + tool)
    info = subprocess.check_output(["go", "version", "-m", exe], text=True)
    if not any(line.split()[:3] == ["mod", "golang.org/x/mobile", MOBILE] for line in info.splitlines()):
        p.error("unexpected " + tool + " version")
@contextmanager
def build_workspace():
    # gomobile embeds the absolute paths of local replacements in both Go binaries.
    work = Path(os.environ.get("NEXUS_AAR_WORKDIR", Path(tempfile.gettempdir()) / "nexus-v2-gomobile")).resolve()
    if work.is_relative_to(ROOT):
        p.error("choose an external NEXUS_AAR_WORKDIR")
    try:
        work.mkdir(mode=0o700)
    except FileExistsError:
        p.error(f"build workspace already exists: {work}; for a parallel build select another NEXUS_AAR_WORKDIR")
    try:
        yield work
    finally:
        shutil.rmtree(work)


with build_workspace() as tmp:
    if a.patched is None:
        a.patched = Path(tmp).resolve() / "patched"
        prepare(a.patched, android=True)
    lib = Path(tmp) / "lib"
    archive = Path(tmp) / "source.zip"
    subprocess.run(["git", "archive", "--format=zip", "-o", archive, LIB], cwd=a.source, check=True)
    with zipfile.ZipFile(archive) as z:
        z.extractall(lib)
    for package in ("internal/congestion", "udp", "transport", "proof", "mobile", "enrollment"):
        dest = lib / {"mobile": "nexusmobile", "enrollment": "enrollment"}.get(package, "internal/nexusv2/" + package)
        shutil.copytree(V2 / package, dest, ignore=shutil.ignore_patterns("*_test.go", "testdata"))
        for f in dest.rglob("*.go"):
            f.write_bytes(f.read_bytes().replace(b"github.com/shejiang000-prog/Nexus/v2/", b"github.com/xtls/libxray/internal/nexusv2/"))
    shutil.copytree(ROOT / "apps/android/engine/v2vpn", lib / "v2vpn", ignore=shutil.ignore_patterns("*_test.go"))
    for flag in ["-require=" + "@".join(QUIC),
                 "-replace=" + QUIC[0] + "=" + (a.patched.resolve()/"aper").as_posix(),
                 "-replace=" + XRAY[0] + "=" + (a.patched.resolve()/"xray").as_posix()]:
        subprocess.run(["go", "mod", "edit", flag], cwd=lib, check=True)
    built = Path(tmp) / "combined.aar"
    subprocess.run(["gomobile", "bind", "-target", "android/arm64,android/amd64", "-androidapi", "21",
                    "-trimpath", "-ldflags=-checklinkname=0 -s -w -buildid= -extldflags=-Wl,-z,max-page-size=16384",
                    "-o", built, ".", "./nexusmobile", "./enrollment", "./v2vpn"], cwd=lib, check=True)
    manifest = json.loads(subprocess.check_output(["go", "mod", "edit", "-json"], cwd=V2))
    checked = {QUIC[0], "github.com/refraction-networking/utls", "golang.org/x/net"}
    expected = {r["Path"]: r["Version"] for r in manifest["Require"] if r["Path"] in checked}
    if expected.keys() != checked:
        p.error("missing transport module pin in v2/go.mod")
    locked = set((V2 / "go.sum").read_text().splitlines())
    with zipfile.ZipFile(built) as aar:
        libraries = [n for n in aar.namelist() if n.startswith("jni/") and n.endswith("/libgojni.so")]
        abis = {"jni/arm64-v8a/libgojni.so": "arm64", "jni/x86_64/libgojni.so": "amd64"}
        if sorted(libraries) != sorted(abis):
            p.error(f"unexpected native libraries: {libraries}")
        for name in libraries:
            binary = aar.extract(name, tmp)
            info = subprocess.check_output(["go", "version", "-m", binary], text=True)
            settings = [line.split() for line in info.splitlines()]
            if ["build", "GOOS=android"] not in settings or ["build", "GOARCH=" + abis[name]] not in settings:
                p.error(f"{name}: incorrect target in build info")
            selected = {}
            previous = None
            patched_quic = False
            for fields in settings:
                if previous in checked and fields[:1] == ["=>"]:
                    if previous != QUIC[0] or Path(fields[1]).resolve() != (a.patched / "aper").resolve():
                        p.error(f"{name}: unexpected replacement for {previous}")
                    patched_quic = True
                previous = fields[1] if len(fields) >= 3 and fields[0] == "dep" else None
                if previous in checked:
                    if previous != QUIC[0] and " ".join(fields[1:]) not in locked:
                        p.error(f"{name}: checksum differs from v2/go.sum for {previous}")
                    selected[fields[1]] = fields[2]
            if not patched_quic:
                p.error(f"{name}: missing patched QUIC replacement")
            if selected != expected:
                p.error(f"{name}: selected {selected}, expected {expected}")
    # Publish only the verified artifact. The staging directory may be on a different drive.
    with tempfile.TemporaryDirectory(dir=output.parent) as stage:
        candidate = Path(stage) / output.name
        shutil.copyfile(built, candidate)
        os.replace(candidate, output)
