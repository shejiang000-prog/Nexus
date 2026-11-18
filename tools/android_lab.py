"""Issue a public-only Android test profile; host a temporary Linux echo target and Nexus server."""
import argparse
import base64
import json
import shutil
import subprocess
import tempfile
import threading
from http.server import ThreadingHTTPServer
from pathlib import Path
from lab_identity import generate
from test_tunnel import EchoHttp, endpoint, process

def lab(args):
    with tempfile.TemporaryDirectory() as tmp, ThreadingHTTPServer(("127.0.0.1", 0), EchoHttp) as http:
        keys = Path(tmp) / "keys"
        generate(keys)
        http.received = []
        threading.Thread(target=http.serve_forever, daemon=True).start()
        (keys / "device.ext").write_text("basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=clientAuth\n")
        subprocess.run([shutil.which("openssl"), "x509", "-new", "-force_pubkey", str(args.pubkey.resolve()),
                        "-subj", "/CN=Nexus Android test device", "-CA", "ca.pem", "-CAkey", "ca.key",
                        "-CAcreateserial", "-days", "2", "-extfile", "device.ext", "-outform", "DER",
                        "-out", "android.der"], cwd=keys, check=True)
        target = endpoint(http.server_port)
        with process([args.server_bin.resolve(), args.host + ":0", "ca.der", "server.der", "server.key.der",
                      "android.der", target], keys) as port:
            def public(name):
                return base64.b64encode((keys / name).read_bytes()).decode("ascii")
            profile = {"version":1, "name":"Debian 测试服务器", "server":f"{args.host}:{port}", "tls_name":"nexus.test",
                       "target":target, "ca_der":public("ca.der"), "device_der":public("android.der")}
            with args.profile.open("x", encoding="utf-8") as output:
                json.dump(profile, output, ensure_ascii=False)
            import hashlib
            print("CA SHA-256", hashlib.sha256((keys / "ca.der").read_bytes()).digest().hex(":"), flush=True)
            print("Profile ready. Press Enter to stop the isolated server.", flush=True)
            input()
        http.shutdown()

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--pubkey", required=True, type=Path)
    parser.add_argument("--profile", required=True, type=Path)
    parser.add_argument("--host", required=True, help="Linux VM interface address reachable from Android")
    parser.add_argument("--server-bin", required=True, type=Path)
    lab(parser.parse_args())
