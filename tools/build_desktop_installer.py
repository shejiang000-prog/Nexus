"""Build the machine-wide x64 installer from the final, already signed payload."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
DESKTOP = ROOT / "apps/desktop"
GITHUB = "https://github.com/tauri-apps/"
TOOLS = {
    "tauri-cli-2.12.6.zip": (GITHUB + "tauri/releases/download/tauri-cli-v2.12.6/cargo-tauri-x86_64-pc-windows-msvc.zip", "39636fa68bc00f13dd610fb57688f292e93742978cc35395f2f1be7c540b88a1"),
    "nsis-3.13.zip": (GITHUB + "binary-releases/releases/download/nsis-3.13/nsis-3.13.zip", "ba63dffc4410ee89193e1cb5a41989991bd77c61068da17e3156d136b7b0b3d8"),
    "nsis_tauri_utils.dll": (GITHUB + "nsis-tauri-utils/releases/download/nsis_tauri_utils-v0.5.3/nsis_tauri_utils.dll", "5ba143b5db4a87d32d6e7802e033330aae56cbceabe0d1e3ba41948385ad4709"),
    "MicrosoftEdgeWebview2Setup.exe": ("https://msedge.sf.dl.delivery.mp.microsoft.com/filestreamingservice/files/cc1101bc-3d8a-4c67-8678-b753ccdc08ad/MicrosoftEdgeWebview2Setup.exe", "fc2972da71d43e0c57b8d85a5cb9b200ed221f723cf43052d08ca722eb0bb22e"),
}
EXTRAS = ("nexus-core.exe", "nexus-tun-helper.exe", "wintun.dll", "Wintun-LICENSE.txt", "Wintun-Go-LICENSE.txt", "WireGuard-Windows-COPYING.txt")


def toolchain(cache, target):
    cache.mkdir(parents=True, exist_ok=True)
    for name, (url, digest) in TOOLS.items():
        path = cache / name
        if not path.is_file():
            with urllib.request.urlopen(url, timeout=60) as source:
                data = source.read()
            if hashlib.sha256(data).hexdigest() != digest:
                raise ValueError("Unexpected download: " + name)
            path.write_bytes(data)
        with path.open("rb") as f:
            if hashlib.file_digest(f, "sha256").hexdigest() != digest:
                raise ValueError("Unexpected cached tool: " + name)
    with zipfile.ZipFile(cache / "tauri-cli-2.12.6.zip") as z:
        cli = cache / "cargo-tauri.exe"
        original = z.read(cli.name)
        if not cli.exists() or cli.read_bytes() != original:
            with tempfile.NamedTemporaryFile(dir=cache, delete=False) as tmp:
                tmp.write(original)
            try:
                os.replace(tmp.name, cli)
            except PermissionError:
                if not cli.exists() or cli.read_bytes() != original:
                    raise
            finally:
                Path(tmp.name).unlink(missing_ok=True)
    with zipfile.ZipFile(cache / "nsis-3.13.zip") as z:
        z.extractall(target)
    (target / "nsis-3.13").rename(target / "NSIS")
    plugins = target / "NSIS/Plugins/x86-unicode/additional"
    plugins.mkdir(exist_ok=True)
    shutil.copyfile(cache / "nsis_tauri_utils.dll", plugins / "nsis_tauri_utils.dll")
    shutil.copyfile(cache / "MicrosoftEdgeWebview2Setup.exe", target / "MicrosoftEdgeWebview2Setup.exe")
    return cache / "cargo-tauri.exe"


def build(payload, version, output, cache):
    if not re.fullmatch(r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)", version) or output.name != f"Nexus-{version}-windows-x64-setup.exe":
        raise ValueError("Expected the versioned x64 setup filename")
    if output.exists():
        raise FileExistsError(output)
    with (payload / "wintun.dll").open("rb") as driver:
        if hashlib.file_digest(driver, "sha256").hexdigest() != "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce":
            raise ValueError("Expected the official Wintun 0.14.1 x64 DLL")
    with tempfile.TemporaryDirectory() as scratch:
        target = Path(scratch)
        cli = toolchain(cache, target / ".tauri")
        release = target / "release"
        release.mkdir()
        shutil.copyfile(payload / "nexus-desktop.exe", release / "nexus-desktop.exe")
        resources = {str(payload / n): n for n in EXTRAS}
        resources[str(ROOT / "THIRD_PARTY_LICENSES.md")] = "licenses/THIRD_PARTY_LICENSES.md"
        resources[str(ROOT / "assets/licenses/desktop-install/NOTICE.txt")] = "licenses/NOTICE.txt"
        for group in ("xray", "split-routing"):
            for f in (ROOT / "apps/android/app/src/main/assets/licenses" / group).iterdir():
                resources[str(f)] = f"licenses/{group}/{f.name}"
        template = (DESKTOP / "installer/setup.nsi").read_text(encoding="utf-8")
        check = (DESKTOP / "installer/check-tree.ps1").read_text(encoding="utf-8")
        encoded = base64.b64encode(check.encode("utf-16-le")).decode("ascii")
        nsi = target / "setup.nsi"
        # NSIS 3.13 limits an individual expanded instruction string to 1024
        # characters. Feed the immutable command in short environment chunks.
        chunks = [encoded[i:i + 600] for i in range(0, len(encoded), 600)]
        setters = []
        for i, chunk in enumerate(chunks):
            setters.extend([f"""System::Call 'kernel32::SetEnvironmentVariableW(w "NEXUS_ACL{i}",w "{chunk}") i .r0'""",
                            "StrCmp $0 0 unsafe_dir"])
        command = "+".join(f"$$env:NEXUS_ACL{i}" for i in range(len(chunks)))
        nsi.write_text(template.replace("@CHECK_ACL_ENV@", "\n  ".join(setters))
                       .replace("@CHECK_ACL_EXPR@", command), encoding="utf-8")
        overrides = {"version": version, "bundle": {"resources": resources,
                     "windows": {"nsis": {"template": str(nsi), "installerIcon": str(DESKTOP / "icons/icon.ico")}}}}
        env = dict(os.environ, CARGO_TARGET_DIR=str(target))
        subprocess.run([cli, "bundle", "--bundles", "nsis", "--ci", "--no-sign", "--no-binary-patching",
                        "--config", json.dumps(overrides)], cwd=DESKTOP, env=env, check=True)
        (installer,) = (release / "bundle/nsis").glob("*-setup.exe")
        output.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(installer, output)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--payload", required=True, type=Path)
    p.add_argument("--version", required=True)
    p.add_argument("--output", required=True, type=Path)
    p.add_argument("--tools-cache", type=Path, default=Path.home() / ".cache/nexus/windows-bundler")
    a = p.parse_args()
    build(a.payload.resolve(), a.version, a.output.resolve(), a.tools_cache.resolve())
