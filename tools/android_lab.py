"""Issue a public-only Android test profile; host a temporary Linux echo target and Nexus server."""
import argparse
import base64
import json
import ipaddress
import secrets
import re
import shutil
import subprocess
import socketserver
import tempfile
import threading
from http.server import ThreadingHTTPServer
from pathlib import Path
from lab_identity import generate
from test_tunnel import EchoHttp, process

class BrowserTarget(EchoHttp):
    def do_GET(self):
        if not re.fullmatch(r"/vpn/[0-9a-f]{8,32}", self.path):
            self.send_error(404)
            return
        receipt = secrets.token_hex(12)
        page = f"<!doctype html><meta name=viewport content='width=device-width'><title>Nexus private node</title><h1>Nexus private node</h1><p>Debian receipt: {receipt}</p>".encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(page)))
        self.end_headers()
        self.wfile.write(page)
        print(f"GET {self.path} peer={self.client_address[0]} receipt={receipt}", flush=True)

class UdpEcho(socketserver.BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        sock.sendto(data, self.client_address)
        print(f"UDP echo {len(data)} bytes peer={self.client_address[0]}", flush=True)

def lab(args):
    bind = args.host if args.dns else "127.0.0.1"
    with tempfile.TemporaryDirectory() as tmp, ThreadingHTTPServer((bind, 0), BrowserTarget) as http, \
            socketserver.UDPServer(http.server_address, UdpEcho) as echo:
        keys = Path(tmp) / "keys"
        generate(keys)
        http.received = []
        threading.Thread(target=http.serve_forever, daemon=True).start()
        threading.Thread(target=echo.serve_forever, daemon=True).start()
        (keys / "device.ext").write_text("basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=clientAuth\n")
        subprocess.run([shutil.which("openssl"), "x509", "-new", "-force_pubkey", str(args.pubkey.resolve()),
                        "-subj", "/CN=Nexus Android test device", "-CA", "ca.pem", "-CAkey", "ca.key",
                        "-CAcreateserial", "-days", "2", "-extfile", "device.ext", "-outform", "DER",
                        "-out", "android.der"], cwd=keys, check=True)
        target = f"{bind}:{http.server_port}"
        policy = ["--internet", f"{args.dns}:53"] if args.dns else []
        with process([args.server_bin.resolve(), args.host + ":0", "ca.der", "server.der", "server.key.der",
                      "android.der", target, *policy], keys) as port:
            def public(name):
                return base64.b64encode((keys / name).read_bytes()).decode("ascii")
            profile = {"version":1, "name":"Debian 测试服务器", "server":f"{args.host}:{port}", "tls_name":"nexus.test",
                       "target":target, "ca_der":public("ca.der"), "device_der":public("android.der")}
            if args.dns:
                profile.update(version=2, dns=str(args.dns))
            with args.profile.open("x", encoding="utf-8") as output:
                json.dump(profile, output, ensure_ascii=False)
            import hashlib
            print("CA SHA-256", hashlib.sha256((keys / "ca.der").read_bytes()).digest().hex(":"), flush=True)
            print("Profile ready. Press Enter to stop the isolated server.", flush=True)
            input()
        http.shutdown()
        echo.shutdown()

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--pubkey", required=True, type=Path)
    parser.add_argument("--profile", required=True, type=Path)
    parser.add_argument("--host", required=True, help="Linux VM interface address reachable from Android")
    parser.add_argument("--server-bin", required=True, type=Path)
    parser.add_argument("--dns", type=ipaddress.IPv4Address, help="Issue a full-device profile with this node-side resolver")
    lab(parser.parse_args())
