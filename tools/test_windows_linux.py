"""Windows CLI -> dedicated WSL2 Debian instance -> Linux echo services / opt-in HTTPS.
Run from the repository root after building the two binaries on their own OS."""
import argparse
import http.client
import json
from pathlib import Path
import queue
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
from http.server import ThreadingHTTPServer
from test_tunnel import EchoHttp, endpoint, post, process
from lab_identity import generate

def linux_peer(args):
    public = None
    if args.public:
        public = socket.gethostbyname("example.com") + ":443"
    with ThreadingHTTPServer(("127.0.0.1", 0), EchoHttp) as httpd, socket.socket(type=socket.SOCK_DGRAM) as udp:
        httpd.received = []
        udp.bind(("127.0.0.1", 0))
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
        def echo():
            while True:
                data, addr = udp.recvfrom(65535)
                udp.sendto(data, addr)
        threading.Thread(target=echo, daemon=True).start()
        tcp_target, udp_target = endpoint(httpd.server_port), endpoint(udp.getsockname()[1])
        # WSL NAT IP is supplied by the calling host; server uses an ephemeral port.
        with process([args.server_bin, args.host + ":0", "ca.der", "server.der", "server.key.der",
                      "device.der", tcp_target, udp_target] + ([public] if public else []), args.identity) as port:
            print(json.dumps([port, tcp_target, udp_target, public]), flush=True)
            sys.stdin.readline()  # parent closes pipe on success, failure or cancellation

def host(args):
    base = ["wsl", "-d", args.distro, "-u", args.user, "--exec"]
    def output(*cmd):
        return subprocess.check_output(base + list(cmd), text=True, encoding="utf-8").strip()
    def mapped(path):
        return output("wslpath", "-u", str(Path(path).resolve()).replace("\\", "/"))
    host_ip = output("hostname", "-I").split()[0]
    client = Path("target/debug/nexus-client.exe").resolve()
    with tempfile.TemporaryDirectory() as tmp:
        identity = Path(tmp) / "keys"
        generate(identity)
        cmd = base + ["python3", mapped(__file__), "--peer", "--host", host_ip,
                      "--identity", mapped(identity), "--server-bin", args.server_bin]
        if args.public:
            cmd.append("--public")
        with subprocess.Popen(cmd, stdout=subprocess.PIPE, stdin=subprocess.PIPE, text=True) as peer:
            try:
                lines = queue.Queue()
                threading.Thread(target=lambda: lines.put(peer.stdout.readline()), daemon=True).start()
                port, tcp_target, udp_target, public = json.loads(lines.get(timeout=30))
                def local(mode, flow, target):
                    return process([client, mode, f"{host_ip}:{port}", "nexus.test", "ca.der",
                                    "device.der", "device.key.der", flow, target, endpoint(0)], identity)
                for mode in ("tls", "quic"):
                    with local(mode, "tcp", tcp_target) as port_local:
                        post(port_local)
                    print("PASS Windows -> Linux HTTP", mode, flush=True)
                    with local(mode, "udp", udp_target) as port_local, socket.socket(type=socket.SOCK_DGRAM) as probe:
                        probe.settimeout(12)
                        probe.sendto(b"Windows to Linux", ("127.0.0.1", port_local))
                        assert probe.recv(1232) == b"Windows to Linux"
                    print("PASS Windows -> Linux UDP", mode, flush=True)
                    if public:
                        with local(mode, "tcp", public) as port_local:
                            with socket.create_connection(("127.0.0.1", port_local), timeout=15) as raw:
                                with ssl.create_default_context().wrap_socket(raw, server_hostname="example.com") as tls:
                                    tls.sendall(b"GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
                                    response = http.client.HTTPResponse(tls)
                                    response.begin()
                                    assert response.status == 200 and b"Example Domain" in response.read(65536)
                        print("PASS Windows -> Linux -> verified example.com HTTPS", mode, flush=True)
            finally:
                peer.stdin.close()
                try:
                    peer.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    peer.kill()

if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("--distro", default="NexusLab")
    p.add_argument("--user", default="nexuslab")
    p.add_argument("--server-bin", default="/var/cache/nexus-build/debug/nexus-server")
    p.add_argument("--public", action="store_true")
    p.add_argument("--peer", action="store_true")
    p.add_argument("--host")
    p.add_argument("--identity")
    args = p.parse_args()
    (linux_peer if args.peer else host)(args)
