"""Create short-lived, disposable credentials in a NEW private test directory."""
import os
import shutil
import subprocess
import sys
from pathlib import Path

def generate(directory):
    directory.mkdir(mode=0o700)
    exe = os.environ.get("OPENSSL") or shutil.which("openssl")
    if not exe:
        raise RuntimeError("Set OPENSSL to the installed openssl executable")

    def run(*args):
        subprocess.run([exe, *args], cwd=directory, check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)

    run("req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
        "-noenc", "-subj", "/CN=Nexus Test CA", "-days", "2",
        "-addext", "basicConstraints=critical,CA:TRUE",
        "-addext", "keyUsage=critical,keyCertSign,cRLSign",
        "-keyout", "ca.key", "-out", "ca.pem")
    run("x509", "-in", "ca.pem", "-outform", "DER", "-out", "ca.der")
    for name in ("server", "device", "other"):
        run("req", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
            "-noenc", "-subj", "/CN=" + name, "-keyout", name + ".key", "-out", name + ".csr")
        extensions = "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\n"
        extensions += ("extendedKeyUsage=serverAuth\nsubjectAltName=DNS:nexus.test\n"
                       if name == "server" else "extendedKeyUsage=clientAuth\n")
        (directory / "cert.ext").write_text(extensions, encoding="ascii")
        run("x509", "-req", "-in", name + ".csr", "-CA", "ca.pem", "-CAkey", "ca.key",
            "-CAcreateserial", "-days", "2", "-extfile", "cert.ext", "-out", name + ".pem")
        run("x509", "-in", name + ".pem", "-outform", "DER", "-out", name + ".der")
        run("pkcs8", "-topk8", "-nocrypt", "-in", name + ".key", "-outform", "DER",
            "-out", name + ".key.der")

if __name__ == "__main__":
    generate(Path(sys.argv[1]))
