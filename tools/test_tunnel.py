"""Exercise compiled client/server binaries. All default targets are ephemeral loopback services."""
import argparse
import http.client
import os
import queue
import socket
import ssl
import struct
import subprocess
import sys
import tempfile
import threading
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from lab_identity import generate

PAYLOAD = bytes(range(256)) * 2048
LOOPBACK = "127.0.0.1"

def endpoint(port):
    return f"{LOOPBACK}:{port}"

@contextmanager
def process(args, cwd):
    with subprocess.Popen(list(map(str, args)), cwd=cwd, stdout=subprocess.PIPE,
                          stdin=subprocess.DEVNULL, text=True) as child:
        try:
            ready = queue.Queue()
            threading.Thread(target=lambda: ready.put(child.stdout.readline()), daemon=True).start()
            line = ready.get(timeout=20).strip()
            assert line.startswith("READY "), f"startup failed: {line!r}"
            yield int(line.rsplit(":", 1)[1])
        finally:
            child.terminate()
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait()

class EchoHttp(BaseHTTPRequestHandler):
    def do_POST(self):
        data = self.rfile.read(int(self.headers["Content-Length"]))
        self.server.received.append(data)
        self.send_response(200)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *args):
        pass

def post(port):
    conn = http.client.HTTPConnection(LOOPBACK, port, timeout=18)
    try:
        conn.request("POST", "/", PAYLOAD)
        response = conn.getresponse()
        assert response.status == 200 and response.read() == PAYLOAD
    finally:
        conn.close()

def run(binaries, identity):
    suffix = ".exe" if os.name == "nt" else ""
    server = binaries / ("nexus-server" + suffix)
    client = binaries / ("nexus-client" + suffix)
    total = 0
    def passed(check):
        nonlocal total
        total += 1
        print("PASS", check, flush=True)

    with ThreadingHTTPServer((LOOPBACK, 0), EchoHttp) as http, socket.socket(type=socket.SOCK_DGRAM) as udp:
        http.received = []
        threading.Thread(target=http.serve_forever, daemon=True).start()
        udp.bind((LOOPBACK, 0))
        udp.settimeout(0.25)
        stop = threading.Event()
        def echo_udp():
            while not stop.is_set():
                try:
                    data, peer = udp.recvfrom(65535)
                    udp.sendto(data, peer)
                except TimeoutError:
                    continue
        threading.Thread(target=echo_udp, daemon=True).start()
        web = endpoint(http.server_port)
        dns = endpoint(udp.getsockname()[1])
        def server_at(port, device="device"):
            return process([server, endpoint(port), "ca.der", "server.der", "server.key.der",
                            device + ".der", web, dns], identity)
        def listener(port, mode, flow, target, device="device", name="nexus.test"):
            return process([client, mode, endpoint(port), name, "ca.der", device + ".der",
                            device + ".key.der", flow, target, endpoint(0)], identity)
        def refused(local):
            before = len(http.received)
            try:
                post(local)
            except ConnectionError:
                assert len(http.received) == before
                return
            raise AssertionError("denied flow delivered unexpected data")
        try:
            with server_at(0) as port:
                for mode in ("tls", "quic"):
                    with listener(port, mode, "tcp", web) as local:
                        post(local)
                        assert http.received[-1] == PAYLOAD
                    passed(mode + " HTTP POST / 512 KiB each direction")
                    with listener(port, mode, "udp", dns) as local, socket.socket(type=socket.SOCK_DGRAM) as probe:
                        probe.settimeout(10)
                        for data in (b"", b"test\x00udp", bytes(range(112)) * 11):
                            probe.sendto(data, (LOOPBACK, local))
                            assert probe.recvfrom(2048)[0] == data
                    passed(mode + " UDP empty, binary and 1232-byte packets")
                    for label, target, device, name in [
                        ("unauthorized device", web, "other", "nexus.test"),
                        ("wrong server name", web, "device", "wrong.test"),
                        ("destination denied", endpoint(1), "device", "nexus.test")]:
                        with listener(port, mode, "tcp", target, device, name) as local:
                            refused(local)
                        passed(mode + " " + label)
                # Exercise the published binary protocol independently of the Rust client.
                ctx = ssl.create_default_context(cafile=str(identity / "ca.pem"))
                ctx.minimum_version = ssl.TLSVersion.TLSv1_3
                ctx.set_alpn_protocols(["nexus/1"])
                ctx.load_cert_chain(identity / "device.pem", identity / "device.key")
                def status(frame):
                    with socket.create_connection((LOOPBACK, port), timeout=4) as raw:
                        with ctx.wrap_socket(raw, server_hostname="nexus.test") as secure:
                            secure.sendall(frame)
                            return secure.recv(1)
                assert status(b"NXUS\x02") == b"\x01"
                assert status(b"NXUS\x01\x01\x04" + socket.inet_aton(LOOPBACK) + struct.pack("!H", 1)) == b"\x02"
                passed("wire version and destination error codes")
                with listener(port, "auto", "tcp", web) as local:
                    post(local)
                passed("automatic transport with QUIC available")
            # Keep the client alive through a server restart and replacement of the authorized leaf.
            with listener(port, "tls", "tcp", web) as original:
                refused(original)
                with server_at(port):
                    post(original)
                with server_at(port, "other"):
                    refused(original)
                    with listener(port, "tls", "tcp", web, "other") as replacement:
                        post(replacement)
                passed("restart, fail closed, reconnect and offline revocation")
            # TCP bridge to the real server; an unserviced UDP socket on the same port drops QUIC.
            import select
            with server_at(0) as upstream, socket.socket() as bridge, socket.socket(type=socket.SOCK_DGRAM) as drop:
                bridge.bind((LOOPBACK, 0))
                bridge.listen()
                blocked = bridge.getsockname()[1]
                drop.bind((LOOPBACK, blocked))
                def forward():
                    with bridge.accept()[0] as left, socket.create_connection((LOOPBACK, upstream)) as right:
                        peers = {left: right, right: left}
                        while peers:
                            for src in select.select(list(peers), [], [], 15)[0]:
                                chunk = src.recv(65536)
                                if chunk:
                                    peers[src].sendall(chunk)
                                else:
                                    peers.pop(src).shutdown(socket.SHUT_WR)
                thread = threading.Thread(target=forward, daemon=True)
                thread.start()
                with listener(blocked, "auto", "tcp", web) as local:
                    post(local)
                thread.join(3)
                passed("UDP dropped, automatic verified TLS/TCP fallback")
        finally:
            stop.set()
            http.shutdown()
    print(f"{total} integration checks passed")

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--bin-dir", type=Path, default=Path("target/debug"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory() as tmp:
        identity = Path(tmp) / "identity"
        generate(identity)
        run(args.bin_dir.resolve(), identity)
