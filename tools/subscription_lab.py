"""Exercise authenticated local peers against a receipt origin, DNS, UDP and two HTTPS sites."""
import contextlib
import hmac
import ipaddress
import json
import secrets
import socket
import socketserver
import struct
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Resolver(socketserver.BaseRequestHandler):
    def handle(self):
        request, sock = self.request
        if len(request) < 17 or request[4:6] != b"\0\1":
            return
        pos = 12
        labels = []
        while pos < len(request) and 0 < request[pos] <= 63:
            size = request[pos]
            labels.append(request[pos + 1:pos + 1 + size].lower())
            pos += 1 + size
        if pos + 5 > len(request) or request[pos]:
            return
        end = pos + 5
        addresses = self.server.hosts.get(b".".join(labels))
        answers = []
        if addresses and request[pos+1:end] == b"\0\1\0\1":
            answers = [b"\xc0\x0c" + struct.pack("!HHIH", 1, 1, 20, 4) + socket.inet_aton(ip) for ip in addresses]
        sock.sendto(request[:2] + struct.pack("!5H", 0x8180 if addresses else 0x8183, 1, len(answers), 0, 0)
                    + request[12:end] + b"".join(answers), self.client_address)


class Echo(socketserver.BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        sock.sendto(data, self.client_address)


class Page(BaseHTTPRequestHandler):
    def do_GET(self):
        if not self.path.startswith("/check/") or len(self.path) > 100:
            return self.send_error(404)
        receipt = self.server.protocol + "-" + hmac.digest(self.server.secret, self.path.encode(), "sha256")[:8].hex()
        body = f"<h1>Nexus VPN receipt</h1><p>{receipt}</p>".encode()
        self.send_response(200)
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
        print(self.path, receipt, flush=True)

    def log_message(self, *args):
        pass


def full_device(args, root, nodes, inbounds, shoes):
    hosts = {host: sorted({record[4][0] for record in socket.getaddrinfo(host.decode(),443,socket.AF_INET,socket.SOCK_STREAM)})
             for host in (b"example.com",b"example.org",b"speed.cloudflare.com")}
    allowed_ips = sorted({ip for records in hosts.values() for ip in records})
    assert all(hosts.values()) and all(ipaddress.ip_address(ip).is_global for ip in allowed_ips)
    with contextlib.ExitStack() as stack:
        def serve(server):
            stack.enter_context(server)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            stack.callback(server.shutdown)
            return server

        dns = serve(socketserver.UDPServer(("127.0.0.1", 0), Resolver))
        dns.hosts = hosts
        udp = serve(socketserver.UDPServer(("127.0.0.1", 0), Echo))
        peers, commands = {}, {}
        def stop(typ):
            proc = peers.pop(typ, None)
            if proc is not None:
                proc.terminate()
                proc.wait(8)

        for number, node in enumerate(nodes):
            typ = node["type"]
            web = serve(ThreadingHTTPServer(("127.0.0.1", 0), Page))
            web.protocol = typ
            web.secret = secrets.token_bytes(32)
            redirects = [("192.0.2.5", 80, web.server_port, "tcp"),
                         ("192.0.2.53", 53, dns.server_address[1], "udp"),
                         ("192.0.2.5", 8053, udp.server_address[1], "udp")]
            if number < len(inbounds):
                outbounds, rules = [], []
                for addr, port, mapped, network in redirects:
                    tag = f"local-{port}"
                    outbounds.append(dict(tag=tag, protocol="freedom", settings=dict(redirect=f"127.0.0.1:{mapped}",
                        finalRules=[dict(action="allow", network=network, ip=["127.0.0.1/32"], port=str(mapped)), dict(action="block")])))
                    rules.append(dict(type="field", ip=[addr], port=str(port), network=network, outboundTag=tag))
                outbounds.extend([dict(tag="web", protocol="freedom", settings=dict(domainStrategy="UseIPv4")),
                                  dict(tag="deny", protocol="blackhole")])
                # The client sniffs TLS and connects by domain (routeOnly=false), so allow the same
                # public test hosts by name as well as by their resolved addresses.
                rules += [dict(type="field", ip=allowed_ips, port="443", network="tcp", outboundTag="web"),
                          dict(type="field", domain=["full:"+h.decode() for h in hosts], port="443", network="tcp", outboundTag="web"),
                          dict(type="field", network="tcp,udp", outboundTag="deny")]
                cfg = dict(log=dict(loglevel="none"), inbounds=[inbounds[number]], outbounds=outbounds,
                           routing=dict(rules=rules))
                binary = [args.xray, "run", "-config"]
            else:
                listener = shoes[number-len(inbounds)]
                rules = [dict(mask=f"{addr}:{port}", action="allow", override_address=f"127.0.0.1:{mapped}",
                              client_chain="direct") for addr, port, mapped, network in redirects]
                rules.extend(dict(mask=f"{ip}:443", action="allow", client_chain="direct") for ip in allowed_ips)
                listener["rules"] = rules + [dict(masks=["0.0.0.0/0","::/0"], action="block")]
                cfg = [listener]
                binary = [args.shoes, "-t", "2", "--no-reload"]
            path = root / (typ + ".json")
            path.write_text(json.dumps(cfg))
            commands[typ] = binary + [path]
            peers[typ] = subprocess.Popen(commands[typ], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            stack.callback(stop, typ)
        time.sleep(1)
        if any(proc.poll() is not None for proc in peers.values()):
            raise RuntimeError("peer startup failed")
        with args.fixture.open("x") as f:
            json.dump(dict(format="nexus-nodes/1", nodes=nodes), f)
        stack.callback(args.fixture.unlink, missing_ok=True)
        print("READY", flush=True)
        while True:
            try:
                command = input().strip()
            except EOFError:
                return
            if command == "quit":
                return
            action, typ = command.split()
            if typ not in commands:
                raise ValueError("unknown fixture")
            if action == "down":
                stop(typ)
            elif action == "up" and typ not in peers:
                peers[typ] = subprocess.Popen(commands[typ], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            else:
                raise ValueError("unexpected fixture state")
            print(action, typ, flush=True)
