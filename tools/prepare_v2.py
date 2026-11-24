"""Materialize verified module zips and audited patches outside the checkout."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
V2 = ROOT / "v2"
QUIC = ("github.com/apernet/quic-go", "v0.65.1-0.20261109005002-2895f039137a")
CADDY = ("github.com/caddyserver/caddy/v2", "v2.11.9")
XRAY = ("github.com/xtls/xray-core", "v1.260327.1-0.20261109052709-676eaf66f8f0")


def unpack(module, dest):
    name, version = module
    with tempfile.TemporaryDirectory() as tmp:
        info = json.loads(subprocess.check_output(
            ["go", "mod", "download", "-json", name + "@" + version], cwd=tmp))
    locked = {tuple(line.split()) for line in (V2 / "go.sum").read_text().splitlines()}
    if (name, version + "/go.mod", info["GoModSum"]) not in locked:
        raise ValueError("unexpected go.mod for " + name)
    prefix = name + "@" + version + "/"
    # Go's HashZip hashes sorted filenames and the SHA-256 of their exact contents.
    with zipfile.ZipFile(info["Zip"]) as z:
        digest = hashlib.sha256()
        for member in sorted(z.namelist()):
            with z.open(member) as src:
                h = hashlib.file_digest(src, "sha256").hexdigest()
            digest.update((h + "  " + member + "\n").encode())
        actual = "h1:" + base64.b64encode(digest.digest()).decode()
        if (name, version, actual) not in locked or actual != info["Sum"]:
            raise ValueError("module zip checksum mismatch: " + name)
        for member in z.infolist():
            if member.is_dir():
                continue
            if not member.filename.startswith(prefix):
                raise ValueError("unexpected module entry")
            path = dest / member.filename[len(prefix):]
            if not path.resolve().is_relative_to(dest):
                raise ValueError("invalid module path")
            path.parent.mkdir(parents=True, exist_ok=True)
            with z.open(member) as src, path.open("xb") as out:
                shutil.copyfileobj(src, out)


def patch(directory, *names):
    for name in names:
        args = ["git", "-c", "core.autocrlf=false", "apply", "--whitespace=error"]
        file = V2 / "patches" / (name + ".patch")
        if not shutil.which("git"):
            subprocess.run(["patch", "--batch", "--forward", "--fuzz=0", "-p1", "-i", file], cwd=directory, check=True)
            continue
        subprocess.run([*args, "--check", file], cwd=directory, check=True)
        subprocess.run([*args, file], cwd=directory, check=True)


def prepare(out, android):
    if out.is_relative_to(ROOT) or out.exists():
        raise ValueError("select a new directory outside the checkout")
    out.mkdir(parents=True)
    try:
        aper, canonical, caddy = (out / s for s in ("aper", "canonical", "caddy"))
        unpack(QUIC, aper)
        patch(aper, "quic-exporter", "quic-context", "quic-handshake-errors", "quic-request-body-close", "quic-tunnel-keepalive", "quic-handshake-progress")
        shutil.copytree(aper, canonical)
        for f in canonical.rglob("*"):
            if f.is_file() and (f.suffix == ".go" or f.name == "go.mod"):
                data = f.read_bytes()
                if QUIC[0].encode() in data:
                    f.write_bytes(data.replace(QUIC[0].encode(), b"github.com/quic-go/quic-go"))
        patch(canonical, "quic-qlog")
        congestion = canonical / "nexus/congestion"
        shutil.copytree(V2 / "internal/congestion", congestion)
        for f in congestion.rglob("*.go"):
            data = f.read_bytes().replace(QUIC[0].encode(), b"github.com/quic-go/quic-go")
            f.write_bytes(data.replace(b"github.com/shejiang000-prog/Nexus/v2/internal/congestion",
                                       b"github.com/quic-go/quic-go/nexus/congestion"))
        unpack(CADDY, caddy)
        patch(caddy, "caddy-datagrams")
        replace = {QUIC[0]: aper, "github.com/quic-go/quic-go": canonical, CADDY[0]: caddy}
        if android:
            core = out / "xray"
            unpack(XRAY, core)
            patch(core, "xray-connect-ip")
            replace[XRAY[0]] = core
        # -modfile isolates the generated local replacements from the tracked module.
        mod = out / "v2.mod"
        shutil.copyfile(V2 / "go.mod", mod)
        shutil.copyfile(V2 / "go.sum", out / "v2.sum")
        for name, path in replace.items():
            subprocess.run(["go", "mod", "edit", f"-replace={name}={path.as_posix()}", mod], check=True)
        print(mod)
    except BaseException:
        shutil.rmtree(out)
        raise


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("output", type=Path)
    p.add_argument("--android", action="store_true", help="also prepare Xray-core")
    args = p.parse_args()
    prepare(args.output.resolve(), args.android)
