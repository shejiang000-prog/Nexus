"""Sign and describe the exact Windows installer that will be uploaded."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


def sign_if_configured(file):
    thumbprint = os.environ.get("NEXUS_WINDOWS_CERT_THUMBPRINT")
    if not thumbprint:
        return False
    if not re.fullmatch(r"[a-fA-F0-9]{40}", thumbprint):
        raise ValueError("Invalid Windows signing certificate thumbprint")
    sdk = os.environ["NEXUS_WINDOWS_SIGNTOOL"]
    subprocess.run([sdk, "sign", "/sha1", thumbprint, "/fd", "SHA256",
                    "/tr", "http://timestamp.digicert.com", "/td", "SHA256", file], check=True)
    subprocess.run([sdk, "verify", "/pa", file], check=True)
    return True


def prepare(tag, setup, cli, config):
    if not re.fullmatch(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)", tag):
        raise ValueError("Expected a stable vX.Y.Z tag")
    version = tag[1:]
    if setup.name != f"Nexus-{version}-windows-x64-setup.exe" or set(setup.parent.iterdir()) != {setup}:
        raise ValueError("Use a fresh output directory containing only the versioned installer")
    signed = sign_if_configured(setup)
    signature = None
    has_key = bool(os.environ.get("TAURI_SIGNING_PRIVATE_KEY") or os.environ.get("TAURI_SIGNING_PRIVATE_KEY_PATH"))
    if has_key != bool(os.environ.get("TAURI_SIGNING_PRIVATE_KEY_PASSWORD")):
        raise ValueError("Windows updater signing requires both the protected key and its password")
    if has_key:
        subprocess.run([cli, "signer", "sign", "--app-version", version, setup], check=True,
                       stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL)
        signature = Path(str(setup) + ".sig").read_text(encoding="ascii").strip()
        # Catch accidentally provisioned CI keys before publishing an un-installable release.
        key = json.loads(config.read_text(encoding="utf-8"))["plugins"]["updater"]["pubkey"]
        pub = base64.b64decode(base64.b64decode(key, validate=True).splitlines()[1], validate=True)
        record = base64.b64decode(signature, validate=True).splitlines()
        sig = base64.b64decode(record[1], validate=True)
        if len(pub) != 42 or len(sig) != 74 or pub[2:10] != sig[2:10] or not record[2].endswith(b"\tversion:" + version.encode()):
            raise ValueError("Updater signing key or signed version differs from the bundled configuration")
        manifest = {"version": version, "platforms": {"windows-x86_64": {
            "url": f"https://github.com/shejiang000-prog/Nexus/releases/download/{tag}/{setup.name}",
            "signature": signature}}}
        (setup.parent / "latest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8", newline="\n")
    notes = "Windows x64 安装包。代码签名" + ("已签名" if signed else "未签名") + "。"
    notes += "自动更新签名" + ("已生成。" if signature else "未配置，仅供手动安装。")
    (setup.parent / "windows-release.txt").write_text(notes + "\n", encoding="utf-8", newline="\n")
    lines = []
    for f in sorted(setup.parent.iterdir()):
        with f.open("rb") as stream:
            lines.append(f"{hashlib.file_digest(stream, 'sha256').hexdigest()}  {f.name}\n")
    (setup.parent / "WINDOWS-SHA256SUMS.txt").write_bytes("".join(lines).encode())


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    sub.add_parser("sign").add_argument("file", type=Path)
    release = sub.add_parser("release")
    release.add_argument("tag")
    release.add_argument("setup", type=Path)
    release.add_argument("--cli", required=True, type=Path)
    args = parser.parse_args()
    if args.action == "sign":
        sign_if_configured(args.file)
    else:
        prepare(args.tag, args.setup.resolve(), args.cli.resolve(),
                Path(__file__).resolve().parents[1] / "apps/desktop/tauri.conf.json")
