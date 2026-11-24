"""Validate the release pair and prepare the files consumed by GitHub Releases."""
import argparse
import hashlib
import re
import shutil
import zipfile
from pathlib import Path


def prepare(tag, apks, nodes, dest, changelog):
    if not re.fullmatch(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)", tag):
        raise ValueError("Expected a stable vX.Y.Z tag")
    version = tag[1:]
    lines = changelog.read_text(encoding="utf-8").splitlines()
    heading = "## " + version
    if lines.count(heading) != 1:
        raise ValueError("Missing or repeated changelog version")
    section = lines[lines.index(heading) + 1:]
    notes = "\n".join(section[:next((i for i,s in enumerate(section) if s.startswith("## ")), len(section))]).strip()
    if not notes:
        raise ValueError("Empty release notes")
    assets = {}
    pins = {}
    for directory, arch in (("x86_64", "amd64"), ("aarch64", "arm64")):
        for binary in ("nexus-node", "nexus-server"):
            source = nodes / directory / binary
            with source.open("rb") as f:
                pins[f"assets/linux/{directory}/{binary}"] = hashlib.file_digest(f, "sha256").hexdigest()
            assets[f"{binary}-linux-{arch}"] = source
    for suffix, expected in [("arm64", {"arm64-v8a"}), ("universal", {"arm64-v8a", "x86_64"})]:
        name = f"Nexus-{version}-{suffix}.apk"
        with zipfile.ZipFile(apks / name) as archive:
            entries = archive.namelist()
            if {n.split("/")[1] for n in entries if n.startswith("lib/") and n.endswith(".so")} != expected:
                raise ValueError(f"Unexpected ABI contents: {name}")
            for abi in expected:
                if not all(f"lib/{abi}/{lib}" in entries for lib in ("libnexus_android.so", "libshoes.so", "libgojni.so")):
                    raise ValueError(f"Incomplete native payload: {name} {abi}")
            for path, digest in pins.items():
                if path in entries or archive.read(path + ".sha256").decode("ascii").strip() != digest:
                    raise ValueError(f"APK must contain only the matching Linux binary digest: {name} {path}")
        assets[name] = apks / name
    dest.mkdir(parents=True, exist_ok=True)
    sums = []
    for name, source in assets.items():
        target = dest / name
        shutil.copyfile(source, target)
        with target.open("rb") as f:
            sums.append(f"{hashlib.file_digest(f, 'sha256').hexdigest()}  {name}")
    (dest / "SHA256SUMS.txt").write_bytes(("\n".join(sums)+"\n").encode())
    (dest / "release-notes.md").write_bytes((notes+"\n").encode())


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("tag")
    parser.add_argument("apks", type=Path)
    parser.add_argument("nodes", type=Path)
    parser.add_argument("dest", type=Path)
    args = parser.parse_args()
    prepare(args.tag, args.apks, args.nodes, args.dest, Path(__file__).resolve().parents[1]/"CHANGELOG.md")
