"""Start authenticated upstream protocol fixtures limited to one loopback HTTP target."""
import argparse
from contextlib import ExitStack
import base64
import hashlib
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import threading
import uuid
from lab_identity import generate

class Receipt(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/check":
            return self.send_error(404)
        data = ("Nexus interop " + secrets.token_hex(8)).encode()
        self.send_response(200)
        self.send_header("Content-Length",str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    def log_message(self,*args):
        pass

def run(args):
    with tempfile.TemporaryDirectory() as tmp, ThreadingHTTPServer(("127.0.0.1",0),Receipt) as http:
        root = Path(tmp)
        keys = root/"keys"
        generate(keys)
        threading.Thread(target=http.serve_forever,daemon=True).start()
        ident = str(uuid.uuid4())
        password = secrets.token_urlsafe(20)
        pin = hashlib.sha256((keys/"server.der").read_bytes()).hexdigest()
        certs = [{"certificateFile":str(keys/"server.pem"),"keyFile":str(keys/"server.key")}]
        inbounds, nodes = [], []
        used = set()
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
            daemon = launch([args.xray,"run","-config",config])
            peers = [daemon]
            if args.shoes:
                settings = []
                for typ in ("tuic","anytls"):
                    with socket.socket(type=socket.SOCK_DGRAM if typ=="tuic" else socket.SOCK_STREAM) as sock:
                        sock.bind((args.host,0))
                        port = sock.getsockname()[1]
                    peer=f"{args.host}:{port}"
                    rule=dict(mask="192.0.2.5:80",action="allow",override_address=f"127.0.0.1:{http.server_port}",client_chain="direct")
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
                    nodes.append(dict(type=typ,name=typ,config=f"{typ}://{auth}@{peer}?sni=nexus.test"+("&alpn=h3" if typ=="tuic" else "")))
                path=root/"shoes.yaml"
                path.write_text(json.dumps(settings))
                peers.append(launch([args.shoes,"-t","2","--no-reload",path]))
            try:
                import time
                time.sleep(0.6)
                if any(proc.poll() is not None for proc in peers):
                    raise RuntimeError("fixture failed to start")
                with args.fixture.open("x") as f:
                    json.dump(dict(target=http.server_port,nodes=nodes,pin=pin),f)
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
    run(parser.parse_args())
