"""Disposable M3 service fixture. Run inside NexusLab with the pinned Xray zip and a locally built V2 node."""
import argparse
import base64
import contextlib
import datetime
import hashlib
import hmac
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import socket
import socketserver
import sqlite3
import subprocess
import threading
import time
import uuid
import zipfile

from lab_identity import generate
from android_v2_netcheck import IdleCapture, UdpShape

TARGET = "192.0.2.85"
TABLE = "nexus_android_m3_" + secrets.token_hex(6)

class TCP(socketserver.ThreadingTCPServer):
    daemon_threads = True

def b64url(data):
    return base64.urlsafe_b64encode(data).decode().rstrip("=")

def serve(node, xray_zip, host, work):
    if os.environ.get("WSL_DISTRO_NAME") != "NexusLab" or not ipaddress.ip_address(host).is_private or host == TARGET:
        raise SystemExit("requires NexusLab and its private interface address")
    os.umask(0o077)
    work.mkdir(mode=0o700)
    keys = work / "keys"
    generate(keys)
    db = work / "ledger.db"
    def ledger(*args):
        return subprocess.check_output([node, "ledger", db, *args], text=True).strip()
    expires = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(hours=8)).strftime("%Y-%m-%dT%H:%M:%SZ")
    user = ledger("create-user", expires)
    with contextlib.ExitStack() as stack:
        def cmd(*args):
            subprocess.run(args, check=True, stdout=subprocess.DEVNULL)
        cmd("ip", "addr", "add", TARGET + "/32", "dev", "lo")
        stack.callback(cmd, "ip", "addr", "del", TARGET + "/32", "dev", "lo")
        cmd("nft", "add", "table", "inet", TABLE)
        stack.callback(cmd, "nft", "delete", "table", "inet", TABLE)
        for chain in ("input", "output"):
            cmd("nft","add","chain","inet",TABLE,chain,"{ type filter hook " + chain + " priority -5; policy accept; }")

        def listener(cls, bind, handler):
            server = stack.enter_context(cls(bind, handler))
            threading.Thread(target=server.serve_forever,daemon=True).start()
            stack.callback(server.shutdown)
            return server.server_address[1]

        class Echo(socketserver.StreamRequestHandler):
            def handle(self):
                self.request.settimeout(150)
                try:
                    while True:
                        tag = self.rfile.read(1)
                        if tag == b"A": self.wfile.write(tag)
                        elif tag == b"D":
                            chunk = bytes([42])*65536
                            for _ in range(64): self.wfile.write(chunk)
                        else: return
                except OSError: pass
        class Datagram(socketserver.BaseRequestHandler):
            def handle(self):
                data, sock = self.request
                sock.sendto(data,self.client_address)
        echo = listener(TCP,(TARGET,0),Echo)
        listener(socketserver.ThreadingUDPServer,(TARGET,echo),Datagram)
        target = f"{TARGET}:{echo}"
        def freeport():
            with socket.socket() as s:
                s.bind((host,0))
                return s.getsockname()[1]
        port = freeport()
        authority = "nexus.test:" + str(port)
        handler = {"handler":"nexus","authority":authority,"ledger_path":str(db),"allow_exact":[target]}
        config = {"admin":{"disabled":True},"apps":{"tls":{"certificates":{"load_files":[{"certificate":str(keys/"server.pem"),"key":str(keys/"server.key")}]}},"http":{"servers":{"node":{"listen":[f"{host}:{port}"],"automatic_https":{"disable":True},"enable_datagrams":True,"tls_connection_policies":[{"protocol_min":"tls1.3"}],"routes":[{"handle":[handler,{"handler":"static_response","status_code":404}]}]}}}}}
        # Store only method/status lines. Never log the connection proof or raw URL.
        access = work / "requests.jsonl"
        config["apps"]["http"]["servers"]["node"]["logs"] = {"default_logger_name":"lab"}
        config["logging"] = {"logs":{"default":{"exclude":["http.log.access"]},
            "access":{"include":["http.log.access"],"writer":{"output":"stdout"}}}}
        def process(binary, args, config, name):
            cfg = work/(name+".json");cfg.write_text(json.dumps(config))
            log = stack.enter_context((work/(name+".log")).open("wb"))
            p = subprocess.Popen([binary,*args,cfg],stdout=subprocess.PIPE if name=="node" else log,
                                 stderr=log,env=dict(os.environ,HOME=str(work)))
            if name == "node":
                # Only persist the HTTP method. Restricted Caddy builds have no filter or file log module.
                def redact():
                    with p.stdout, access.open("w",buffering=1) as out:
                        for line in p.stdout:
                            try: method=json.loads(line)["request"]["method"]
                            except (ValueError,KeyError):continue
                            out.write(json.dumps({"request":{"method":method}})+"\n")
                reader=threading.Thread(target=redact,daemon=True)
                reader.start()
                stack.callback(reader.join,2)
            def stop():
                if p.poll() is None:
                    p.terminate()
                    try:p.wait(6)
                    except subprocess.TimeoutExpired:p.kill();p.wait()
            stack.callback(stop)
            return p
        node_process = process(node,["run","--config"],config,"node")
        with xray_zip.open("rb") as stream:
            if hashlib.file_digest(stream,"sha256").hexdigest()!="b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82":
                raise ValueError("unapproved Xray test asset")
        with zipfile.ZipFile(xray_zip) as z: binary = Path(z.extract("xray",work))
        binary.chmod(0o700)
        xport = freeport(); client = str(uuid.uuid4())
        xc = {"inbounds":[{"listen":host,"port":xport,"protocol":"vless","settings":{"clients":[{"id":client}],"decryption":"none"}}],"outbounds":[{"protocol":"blackhole","tag":"deny"},{"protocol":"freedom","tag":"echo"}],"routing":{"rules":[{"ip":[TARGET],"port":str(echo),"outboundTag":"echo"}]},"log":{"loglevel":"none"}}
        xc["outbounds"][1]["settings"] = {"finalRules":[
            {"action":"allow","ip":[TARGET + "/32"],"port":str(echo)},
            {"action":"block"}]}
        xr=process(binary,["run","-config"],xc,"xray-server")
        lock = threading.Lock()
        ctl = secrets.token_urlsafe(32)
        nic = next(row["ifname"] for row in json.loads(subprocess.check_output(["ip","-j","-4","addr"]))
                   if any(a["local"] == host for a in row["addr_info"]))
        shape = UdpShape(nic, host, port)
        stack.callback(shape.close)
        capture = IdleCapture(nic, host, port, access, work)
        stack.callback(capture.close)
        def switch(op):
            shape.change(op)
            batch = [f"flush chain inet {TABLE} {c}" for c in ("input","output")]
            if op == "drop":
                batch += [f"add rule inet {TABLE} input ip daddr {host} udp dport {port} counter drop",
                          f"add rule inet {TABLE} output ip saddr {host} udp sport {port} counter drop"]
            subprocess.run(["nft","-f","-"],input="\n".join(batch)+"\n",text=True,check=True)
        class Control(socketserver.StreamRequestHandler):
            def handle(self):
                self.request.settimeout(3)
                try:
                    words=self.rfile.readline(128).decode("ascii").strip().split(" ")
                    if len(words)<2 or not hmac.compare_digest(ctl,words[0]):return
                    with lock:
                        if len(words)==2 and words[1] in ("clear","drop","rate","loss"):switch(words[1])
                        elif len(words)==2 and words[1] in ("capture-h2","capture-h3"):capture.start(words[1][-2:])
                        elif words[1:] == ["capture-stop"]:capture.finish()
                        elif len(words)==3 and words[1]=="revoke" and re.fullmatch("[0-9a-f]{32}",words[2]):ledger("revoke",words[2])
                        elif len(words)==3 and words[1]=="extend" and re.fullmatch("[0-9a-f]{32}",words[2]):
                            with contextlib.closing(sqlite3.connect(db)) as conn, conn:
                                changed = conn.execute("""UPDATE users SET expires_at=expires_at+86400
                                    WHERE id=(SELECT i.user_id FROM devices d JOIN invites i ON i.digest=d.invite_digest
                                    WHERE d.id=?)""", (bytes.fromhex(words[2]),)).rowcount
                                if changed != 1: return
                        else:return
                    self.wfile.write(b"\x01")
                except (ValueError,OSError,subprocess.SubprocessError):return
        cport=listener(TCP,(host,0),Control)
        invite=ledger("invite",user,expires)
        link="nexus://invite/v2#"+b64url(json.dumps(dict(v=2,host="nexus.test",port=port,invite=invite,name="NexusLab V2")).encode())
        sub=f"vless://{client}@{host}:{xport}?encryption=none&security=none&type=tcp#M3-Lab"
        for peer in (port,xport):
            for _ in range(100):
                if node_process.poll() is not None or xr.poll() is not None:raise RuntimeError("lab process exited")
                try:
                    with socket.create_connection((host,peer),.15):break
                except OSError:time.sleep(.1)
            else:raise RuntimeError("lab listeners not ready")
        (work/"device.json").write_text(json.dumps(dict(invitation=link,endpoint=f"{host}:{port}",host=host,ca=(keys/"ca.pem").read_text(),echoPort=echo,target=TARGET,controlPort=cport,control=ctl,subscription=sub)))
        print("M3 fixture ready",flush=True)
        while not (work/"stop").is_file():
            if node_process.poll()!=None or xr.poll()!=None:raise RuntimeError("lab process exited")
            time.sleep(.5)

if __name__ == "__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("node",type=Path)
    p.add_argument("xray_zip",type=Path)
    p.add_argument("host")
    p.add_argument("work",type=Path)
    a=p.parse_args()
    serve(a.node.resolve(),a.xray_zip.resolve(),a.host,a.work.resolve())
