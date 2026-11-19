"""Exercise compiled client/server binaries. All default targets are ephemeral loopback services."""
import argparse
import http.client
import os
import queue
import socket
import ssl
import struct
import sqlite3
import secrets
import subprocess
import sys
import tempfile
import threading
import time
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

def dual_port():
    # TCP and UDP have different excluded ranges on Windows hosts running Hyper-V.
    for _ in range(32):
        with socket.socket(type=socket.SOCK_DGRAM) as udp, socket.socket() as tcp:
            port = 20000 + secrets.randbelow(28000)
            try:
                udp.bind((LOOPBACK,port))
                tcp.bind((LOOPBACK,port))
                return port
            except OSError:
                continue
    raise RuntimeError("no local port available for both protocols")

@contextmanager
def count_quic_connections(server_port):
    # Observe the server SCID on v1 Initial packets, including retransmissions. No keys required.
    ids = set()
    with socket.socket(type=socket.SOCK_DGRAM) as sock:
        sock.bind((LOOPBACK, 0))
        sock.settimeout(.1)
        stop = threading.Event()
        def forward():
            client = None
            while not stop.is_set():
                try:
                    data, peer = sock.recvfrom(65535)
                except (TimeoutError, ConnectionResetError):
                    continue
                if peer == (LOOPBACK, server_port):
                    if data[0] & 0xb0 == 0x80 and data[1:5] == bytes.fromhex("00000001"):
                        offset = 6 + data[5]
                        ids.add(data[offset+1:offset+1+data[offset]])
                    sock.sendto(data, client)
                else:
                    client = peer
                    sock.sendto(data, (LOOPBACK,server_port))
        thread = threading.Thread(target=forward,daemon=True)
        thread.start()
        try:
            yield sock.getsockname()[1], ids
        finally:
            stop.set()
            thread.join(2)

def run(binaries, identity, baseline=None):
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
            return process([server, endpoint(port or dual_port()), "ca.der", "server.der", "server.key.der",
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
            driver = binaries / "examples" / ("udp_associations" + suffix)
            if not driver.exists():
                raise RuntimeError("build the matching UDP driver: cargo build --example udp_associations")
            with server_at(0) as port, count_quic_connections(port) as (proxy, ids):
                out = subprocess.check_output([driver,endpoint(proxy),dns,"64","4","check"],cwd=identity,text=True,timeout=40)
                assert len(out.splitlines()) == 255 and len(ids) == 1, (len(out.splitlines()),len(ids))
            passed("mux2: 64 concurrent associations on one QUIC connection; empty/1472 frames; siblings survive close")

            if baseline:
                for older in ("server","client"):
                    srv = baseline / server.name if older=="server" else server
                    cli = baseline / client.name if older=="client" else client
                    with process([srv,endpoint(dual_port()),"ca.der","server.der","server.key.der","device.der",dns],identity) as port:
                        with process([cli,"quic",endpoint(port),"nexus.test","ca.der","device.der","device.key.der","udp",dns,endpoint(0)],identity) as local:
                            with socket.socket(type=socket.SOCK_DGRAM) as probe:
                                probe.settimeout(10)
                                for data in (b"legacy",bytes(1472),b""):
                                    probe.sendto(data,(LOOPBACK,local))
                                    assert probe.recv(2048)==data
                    passed("udp1 interoperability with baseline "+older)
            else:
                print("SKIP baseline binary compatibility (set NEXUS_BASELINE_BIN or --baseline-dir)")

            with sqlite3.connect(identity/"mux.db") as db:
                db.execute("CREATE TABLE devices(cert BLOB PRIMARY KEY, expires INTEGER, quota INTEGER, used INTEGER, revoked INTEGER)")
                db.execute("INSERT INTO devices VALUES(?,unixepoch()+600,16384,0,0)",((identity/"device.der").read_bytes(),))
                db.commit()
                with process([server,endpoint(dual_port()),"ca.der","server.der","server.key.der","registry:mux.db",dns,web],identity) as port:
                    with subprocess.Popen([driver,endpoint(port),dns,"16","1","revoke",web],cwd=identity,stdout=subprocess.PIPE,text=True) as child:
                        try:
                            lines = queue.Queue()
                            def collect():
                                for line in child.stdout: lines.put(line)
                                lines.put("")
                            threading.Thread(target=collect,daemon=True).start()
                            active=0
                            while active<17:
                                line=lines.get(timeout=12)
                                assert line, "premature exit"
                                active+=line.startswith("ACTIVE ")
                            db.execute("UPDATE devices SET revoked=1")
                            db.commit()
                            assert child.wait(timeout=10)==0
                        finally:
                            if child.poll() is None: child.kill()
                    for _ in range(100):
                        if db.execute("SELECT used FROM devices").fetchone()[0]==2048: break
                        time.sleep(.03)
                    assert db.execute("SELECT used FROM devices").fetchone()==(2048,)
            db.close()
            passed("mux2 registry: 16 live associations and TCP stream revoked; exact 2048 payload bytes after refund")
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
                bridge.bind((LOOPBACK, dual_port()))
                bridge.listen()
                blocked = bridge.getsockname()[1]
                drop.bind((LOOPBACK, blocked))
                def forward():
                    for _ in range(2):
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
                    measurements = []
                    for _ in range(2):
                        started = time.monotonic()
                        post(local)
                        measurements.append(time.monotonic()-started)
                    print("UDP blocked, two authenticated requests: %.2fs, %.2fs" % tuple(measurements),flush=True)
                    assert measurements[1] < 5, "repeated a full QUIC setup timeout on the same blocked path"
                thread.join(3)
                passed("UDP dropped, automatic verified TLS/TCP fallback")
        finally:
            stop.set()
            http.shutdown()
    print(f"{total} integration checks passed")

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--bin-dir", type=Path, default=Path("target/debug"))
    parser.add_argument("--baseline-dir",type=Path,default=os.environ.get("NEXUS_BASELINE_BIN"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory() as tmp:
        identity = Path(tmp) / "identity"
        generate(identity)
        run(args.bin_dir.resolve(), identity, args.baseline_dir.resolve() if args.baseline_dir else None)
