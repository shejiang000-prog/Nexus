"""Start authenticated upstream protocol fixtures limited to one loopback HTTP target."""
import argparse
from contextlib import ExitStack
import base64
import hashlib
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import re
import secrets
import socket
import subprocess
import tempfile
import threading
import uuid
from lab_identity import generate

class Receipt(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/slow":
            import time
            time.sleep(3)
        if self.path != "/check":
            return self.send_error(404)
        data = ("Nexus interop " + secrets.token_hex(8)).encode()
        self.send_response(200)
        self.send_header("Content-Length",str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    def log_message(self,*args):
        pass

def variants(args, keys, ident, password, pin, free_port):
    """Transports and formats subscriptions use beyond plain share links: Clash/mihomo and
    sing-box structured nodes, REALITY with Vision, CDN transports, SS-2022 and Hysteria2 obfs."""
    certs = [{"certificateFile": str(keys/"server.pem"), "keyFile": str(keys/"server.key")}]
    tls = {"security": "tls", "tlsSettings": {"certificates": certs}}
    pair = subprocess.run([args.xray, "x25519"], capture_output=True, text=True, check=True).stdout
    private = re.search(r"PrivateKey:\s*(\S+)", pair).group(1)
    public = re.search(r"(?:Password \(PublicKey\)|Password|Public key):\s*(\S+)", pair).group(1)
    sid = secrets.token_hex(4)
    rows = []
    # REALITY borrows the target's handshake and needs each encrypted handshake message in its own
    # record, as Go and most production servers send them. OpenSSL coalesces them, so the decoy
    # site is a Go TLS listener inside the same Xray process. Clients never send data to it.
    decoy = free_port()
    rows.append((dict(listen=args.host, port=decoy, protocol="vless",
                      settings={"clients": [{"id": str(uuid.uuid4())}], "decryption": "none"},
                      streamSettings=dict(tls, tlsSettings={"certificates": certs, "alpn": ["h2", "http/1.1"]})), None))
    reality = {"security": "reality", "realitySettings": {"target": f"{args.host}:{decoy}", "serverNames": ["nexus.test"],
                                                          "privateKey": private, "shortIds": [sid]}}
    obfs = secrets.token_urlsafe(12)
    ss_key = base64.b64encode(secrets.token_bytes(16)).decode()
    pinned = {"servername": "nexus.test", "fingerprint": pin}
    vless = {"clients": [{"id": ident}], "decryption": "none"}
    vision = {"clients": [{"id": ident, "flow": "xtls-rprx-vision"}], "decryption": "none"}

    def add(name, protocol, settings, stream, node_type, config, udp=False):
        port = free_port(udp)
        config = dict(config, server=args.host)
        config["server_port" if "server_port" in config else "port"] = port
        rows.append((dict(listen=args.host, port=port, protocol=protocol, settings=settings, streamSettings=stream),
                     dict(type=node_type, name=name, config=config)))

    add("vless-reality-clash", "vless", vision, dict(network="raw", **reality), "vless",
        {"type": "vless", "uuid": ident, "network": "tcp", "tls": True, "flow": "xtls-rprx-vision",
         "servername": "nexus.test", "reality-opts": {"public-key": public, "short-id": sid},
         "client-fingerprint": "chrome", "port": 0})
    add("vless-reality-singbox", "vless", vision, dict(network="raw", **reality), "vless",
        {"type": "vless", "uuid": ident, "flow": "xtls-rprx-vision", "server_port": 0,
         "tls": {"enabled": True, "server_name": "nexus.test", "utls": {"enabled": True, "fingerprint": "firefox"},
                 "reality": {"enabled": True, "public_key": public, "short_id": sid}}})
    # No servername: mihomo takes SNI from the WebSocket Host header.
    add("vless-ws-tls", "vless", vless, dict(network="ws", wsSettings={"path": "/qa-ws", "host": "nexus.test"}, **tls),
        "vless", {"type": "vless", "uuid": ident, "tls": True, "fingerprint": pin, "network": "ws", "port": 0,
                  "ws-opts": {"path": "/qa-ws", "headers": {"Host": "nexus.test"}}})
    add("vmess-grpc-tls", "vmess", {"clients": [{"id": ident}]},
        dict(network="grpc", grpcSettings={"serviceName": "qa-grpc"}, **tls), "vmess",
        dict(pinned, type="vmess", uuid=ident, alterId=0, cipher="auto", tls=True, network="grpc", port=0,
             **{"grpc-opts": {"grpc-service-name": "qa-grpc"}}))
    add("trojan-httpupgrade", "trojan", {"clients": [{"password": password}]},
        dict(network="httpupgrade", httpupgradeSettings={"path": "/qa-up", "host": "nexus.test"}, **tls), "trojan",
        {"type": "trojan", "password": password, "sni": "nexus.test", "fingerprint": pin, "network": "ws", "port": 0,
         "ws-opts": {"path": "/qa-up", "headers": {"Host": "nexus.test"}, "v2ray-http-upgrade": True}})
    add("vless-xhttp-tls", "vless", vless, dict(network="xhttp", xhttpSettings={"path": "/qa-xhttp"}, **tls), "vless",
        dict(pinned, type="vless", uuid=ident, tls=True, network="xhttp", port=0,
             **{"xhttp-opts": {"path": "/qa-xhttp", "mode": "packet-up"}}))
    add("vmess-http-header", "vmess", {"clients": [{"id": ident}]},
        dict(network="raw", rawSettings={"header": {"type": "http", "request": {
            "path": ["/qa-http"], "headers": {"Host": ["nexus.test"]}}}}), "vmess",
        {"type": "vmess", "uuid": ident, "alterId": 0, "cipher": "auto", "network": "http", "port": 0,
         "http-opts": {"method": "GET", "path": ["/qa-http"], "headers": {"Host": ["nexus.test"]}}})
    add("ss-2022-singbox", "shadowsocks",
        {"method": "2022-blake3-aes-128-gcm", "password": ss_key, "network": "tcp,udp"}, {}, "ss",
        {"type": "shadowsocks", "method": "2022-blake3-aes-128-gcm", "password": ss_key, "server_port": 0})
    hy2 = dict(network="hysteria", hysteriaSettings={"version": 2},
               security="tls", tlsSettings={"certificates": certs, "alpn": ["h3"]},
               finalmask={"udp": [{"type": "salamander", "settings": {"password": obfs}}]})
    add("hy2-salamander", "hysteria", {"version": 2, "users": [{"auth": password}]}, hy2, "hysteria2",
        dict(pinned, type="hysteria2", password=password, sni="nexus.test", obfs="salamander", port=0,
             **{"obfs-password": obfs}), udp=True)
    return rows


def run(args):
    with tempfile.TemporaryDirectory() as tmp, ThreadingHTTPServer(("127.0.0.1",0),Receipt) as http:
        root = Path(tmp)
        keys = root/"keys"
        generate(keys)
        if args.probe:
            import ssl
            tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            tls.load_cert_chain(keys/"server.pem", keys/"server.key")
            http.socket = tls.wrap_socket(http.socket, server_side=True)
        threading.Thread(target=http.serve_forever,daemon=True).start()
        ident = str(uuid.uuid4())
        password = secrets.token_urlsafe(20)
        pin = hashlib.sha256((keys/"server.der").read_bytes()).hexdigest()
        certs = [{"certificateFile":str(keys/"server.pem"),"keyFile":str(keys/"server.key")}]
        inbounds, nodes = [], []
        used = set()
        def free_port(udp=False):
            with socket.socket(type=socket.SOCK_DGRAM if udp else socket.SOCK_STREAM) as s:
                s.bind((args.host,0))
                port = s.getsockname()[1]
            assert port not in used
            used.add(port)
            return port
        for typ in ["vless","vmess","trojan","ss","hysteria2"]:
            with socket.socket(type=socket.SOCK_DGRAM if typ=="hysteria2" else socket.SOCK_STREAM) as s:
                s.bind((args.host,0))
                port = s.getsockname()[1]
            assert port not in used
            used.add(port)
            peer=f"{args.host}:{port}"
            stream = {}
            if typ=="vless":
                settings={"clients":[{"id":ident}],"decryption":"none"}
                link=f"vless://{ident}@{peer}?encryption=none"
            elif typ=="vmess":
                settings={"clients":[{"id":ident}]}
                link=f"vmess://{ident}@{peer}?encryption=auto"
            elif typ=="ss":
                settings={"method":"aes-128-gcm","password":password,"network":"tcp,udp"}
                auth=base64.urlsafe_b64encode(("aes-128-gcm:"+password).encode()).decode().rstrip("=")
                link=f"ss://{auth}@{peer}"
            else:
                stream={"security":"tls","tlsSettings":{"certificates":certs}}
                if typ=="trojan":
                    settings={"clients":[{"password":password}]}
                    link=f"trojan://{password}@{peer}?security=tls&sni=nexus.test&pcs={pin}"
                else:
                    stream.update(network="hysteria",hysteriaSettings={"version":2})
                    stream["tlsSettings"]["alpn"]=["h3"]
                    settings={"version":2,"users":[{"auth":password}]}
                    link=f"hysteria2://{password}@{peer}?sni=nexus.test&pcs={pin}"
            inbounds.append(dict(listen=args.host,port=port,protocol={"ss":"shadowsocks","hysteria2":"hysteria"}.get(typ,typ),
                                 settings=settings,streamSettings=stream))
            nodes.append(dict(type=typ,name=typ,config=link+"#"+typ))
        if args.variants:
            for inbound, node in variants(args, keys, ident, password, pin, free_port):
                inbounds.append(inbound)
                if node:
                    nodes.append(node)
        # All authenticated test flows terminate at the same ephemeral local receipt service.
        cfg=dict(log={"loglevel":"none"},inbounds=inbounds,
                 outbounds=[dict(protocol="freedom",settings={"redirect":f"127.0.0.1:{http.server_port}",
                     "finalRules":[{"action":"allow","network":"tcp","ip":["127.0.0.1/32"],"port":str(http.server_port)},
                                   {"action":"block"}]})])
        config=root/"xray.json"
        config.write_text(json.dumps(cfg))
        with ExitStack() as stack:
            def launch(command):
                proc = subprocess.Popen(command,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
                stack.callback(proc.wait,5)
                stack.callback(proc.terminate)
                return proc
            peers = [] if args.full_device else [launch([args.xray,"run","-config",config])]
            if args.shoes:
                settings = []
                for typ in ("tuic","anytls"):
                    with socket.socket(type=socket.SOCK_DGRAM if typ=="tuic" else socket.SOCK_STREAM) as sock:
                        sock.bind((args.host,0))
                        port = sock.getsockname()[1]
                    peer=f"{args.host}:{port}"
                    rule=dict(mask="nexus.test:443" if args.probe else "192.0.2.5:80",action="allow",override_address=f"127.0.0.1:{http.server_port}",client_chain="direct")
                    listener=dict(address=peer,rules=[rule,{"masks":"0.0.0.0/0","action":"block"}])
                    cert=dict(cert=str(keys/"server.pem"),key=str(keys/"server.key"))
                    if typ=="tuic":
                        listener.update(transport="quic",quic_settings=dict(cert,alpn_protocols=["h3"]),protocol=dict(type="tuic",uuid=ident,password=password))
                        auth=ident+":"+password
                    else:
                        tls=dict(cert,protocol=dict(type="anytls",users=[dict(name="qa",password=password)]))
                        listener.update(protocol=dict(type="tls",tls_targets={"nexus.test":tls}))
                        auth=password
                    settings.append(listener)
                    nodes.append(dict(type=typ,name=typ,config=f"{typ}://{auth}@{peer}?sni=nexus.test" + (f"&pcs={pin}" if args.full_device or args.probe else "") +("&alpn=h3" if typ=="tuic" else "")))
                path=root/"shoes.yaml"
                path.write_text(json.dumps(settings))
                if args.full_device:
                    from subscription_lab import full_device
                    return full_device(args, root, nodes, inbounds, settings)
                peers.append(launch([args.shoes,"-t","2","--no-reload",path]))
            try:
                import time
                time.sleep(0.6)
                if any(proc.poll() is not None for proc in peers):
                    raise RuntimeError("fixture failed to start")
                with args.fixture.open("x") as f:
                    json.dump(dict(target=http.server_port,nodes=nodes,pin=pin,ca=(keys/"ca.pem").read_text() if args.probe else ""),f)
                print(f"{len(nodes)} authenticated fixtures ready; press Enter to stop.",flush=True)
                input()
            finally:
                args.fixture.unlink(missing_ok=True)
        http.shutdown()

if __name__=="__main__":
    import os
    os.umask(0o077)
    parser=argparse.ArgumentParser()
    parser.add_argument("--host",required=True)
    parser.add_argument("--xray",required=True,type=Path)
    parser.add_argument("--shoes",type=Path)
    parser.add_argument("--fixture",required=True,type=Path)
    parser.add_argument("--probe", action="store_true", help="serve a lab-CA HTTPS response for batch probes")
    parser.add_argument("--full-device",action="store_true")
    parser.add_argument("--variants",action="store_true",
                        help="add REALITY, CDN transports, SS-2022, Hysteria2 obfs and structured nodes")
    args = parser.parse_args()
    if args.full_device and not args.shoes: parser.error("--full-device requires --shoes")
    if args.full_device and args.variants: parser.error("--variants runs in the node-side receipt mode")
    run(args)
