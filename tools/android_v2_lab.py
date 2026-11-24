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
import subprocess
import sys
import threading
import time
import uuid
import zipfile

from lab_identity import generate
from android_v2_netcheck import IdleCapture, UdpShape
from android_v2_observe import NodeSamples, installed_echo

TARGET = "192.0.2.85"
TABLE = "nexus_android_m3_" + secrets.token_hex(6)

class TCP(socketserver.ThreadingTCPServer):
    daemon_threads = True

def b64url(data):
    return base64.urlsafe_b64encode(data).decode().rstrip("=")

def issue(node,work,name):
    """Create a private configuration for another one-time instrumentation run."""
    if os.environ.get("WSL_DISTRO_NAME")!="NexusLab" or Path(name).name!=name or not name.endswith(".json"):
        raise ValueError("expected a new JSON filename in the private NexusLab fixture")
    os.umask(0o077)
    cfg=json.loads((work/"device.json").read_bytes())
    data=json.loads(base64.urlsafe_b64decode(cfg["invitation"].split("#")[1]+"==="))
    db=work/"ledger.db" if "pin" not in data else Path("/var/lib/nexus-v2/ledger.db")
    if not db.is_file():raise ValueError("fixture ledger unavailable")
    def cmd(*args):return subprocess.check_output([node,"ledger",db,*args],text=True).strip()
    expires=(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(hours=6)).strftime("%Y-%m-%dT%H:%M:%SZ")
    with (work/name).open("x") as f:
        user=cmd("create-user",expires)
        if "pin" in data: cfg["invitation"]=cmd("invite","--link",user,expires)
        else:
            data["invite"]=cmd("invite",user,expires)
            cfg["invitation"]="nexus://invite/v2#"+b64url(json.dumps(data).encode())
        json.dump(cfg,f)

def serve(node, xray_zip, host, work, installed_port=None, echo_ip=TARGET):
    if os.environ.get("WSL_DISTRO_NAME") != "NexusLab" or not ipaddress.ip_address(host).is_private or host == echo_ip:
        raise SystemExit("requires NexusLab and its private interface address")
    if ipaddress.ip_address(echo_ip) not in ipaddress.ip_network("192.0.2.0/24"):
        raise ValueError("choose a TEST-NET-1 echo address")
    os.umask(0o077)
    work.mkdir(mode=0o700)
    keys = work / "keys"
    if installed_port is None: generate(keys)
    db = work / "ledger.db" if installed_port is None else Path("/var/lib/nexus-v2/ledger.db")
    if installed_port is not None and not db.is_file():
        raise ValueError("expected an installed test ledger")
    def ledger(*args):
        return subprocess.check_output([node, "ledger", db, *args], text=True).strip()
    expires = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(hours=8)).strftime("%Y-%m-%dT%H:%M:%SZ")
    user = ledger("create-user", expires)
    with contextlib.ExitStack() as stack:
        def cmd(*args):
            subprocess.run(args, check=True, stdout=subprocess.DEVNULL)
        cmd("ip", "addr", "add", echo_ip + "/32", "dev", "lo")
        stack.callback(cmd, "ip", "addr", "del", echo_ip + "/32", "dev", "lo")
        def listener(cls, bind, handler):
            server = stack.enter_context(cls(bind, handler))
            threading.Thread(target=server.serve_forever,daemon=True).start()
            stack.callback(server.shutdown)
            return server.server_address[1]

        marks={}
        marks_lock=threading.Lock()
        class Echo(socketserver.StreamRequestHandler):
            def handle(self):
                self.request.settimeout(150)
                try:
                    while True:
                        tag = self.rfile.read(1)
                        if tag == b"A": self.wfile.write(tag)
                        elif tag in (b"N",b"Q"):
                            marker=self.rfile.read(8)
                            if len(marker)!=8:return
                            with marks_lock:
                                if tag==b"N":marks[marker]=marks.get(marker,0)+1
                                else:self.wfile.write(marks.get(marker,0).to_bytes(4,"big"))
                        elif tag == b"D":
                            chunk = bytes([42])*65536
                            for _ in range(64): self.wfile.write(chunk)
                        else: return
                except OSError: pass
        class Datagram(socketserver.BaseRequestHandler):
            def handle(self):
                data, sock = self.request
                sock.sendto(data,self.client_address)
        echo = listener(TCP,(echo_ip,0),Echo)
        listener(socketserver.ThreadingUDPServer,(echo_ip,echo),Datagram)
        target = f"{echo_ip}:{echo}"
        def freeport():
            with socket.socket() as s:
                s.bind((host,0))
                return s.getsockname()[1]
        port = installed_port if installed_port is not None else freeport()
        authority = (host if installed_port is not None else "nexus.test") + ":" + str(port)
        handler = {"handler":"nexus","authority":authority,"ledger_path":str(db),"allow_exact":[target]}
        config = {"admin":{"disabled":True},"apps":{"tls":{"certificates":{"load_files":[{"certificate":str(keys/"server.pem"),"key":str(keys/"server.key")}]}},"http":{"servers":{"node":{"listen":[f"{host}:{port}"],"automatic_https":{"disable":True},"enable_datagrams":True,"tls_connection_policies":[{"protocol_min":"tls1.3"}],"routes":[{"handle":[handler,{"handler":"static_response","status_code":404}]}]}}}}}
        local_admin = work / "admin.sock"
        config["admin"]={"listen":"unix/"+str(local_admin)}
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
                # Only persist method, protocol, timings and numerical test counters.
                def redact():
                    with p.stdout, access.open("w",buffering=1) as out:
                        for line in p.stdout:
                            try:
                                payload=json.loads(line)
                                req=payload["request"]
                                row={"request":{"method":req["method"],"proto":req["proto"]},
                                     "ts":payload["ts"],"duration":payload["duration"]}
                                for name,values in payload.get("resp_headers",{}).items():
                                    if name.lower() in ("nexus-quic-sent","nexus-quic-lost"):
                                        row[name.lower()]=int(values[0])
                            except (ValueError,KeyError):continue
                            out.write(json.dumps(row)+"\n")
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
        if installed_port is None:
            node_process = process(node,["run","--config"],config,"node")
            pid=node_process.pid
        else:
            node_process=None
            local_admin=Path("/var/lib/nexus-v2/admin.sock")
            stack.enter_context(installed_echo(local_admin,authority,target))
            pid=int(subprocess.check_output(["systemctl","show","-p","MainPID","--value","nexus-v2"]))
            if pid<=1:raise ValueError("installed fixture unit is not running")
        samples=NodeSamples(pid,local_admin,echo,work/"node-samples.jsonl")
        with xray_zip.open("rb") as stream:
            if hashlib.file_digest(stream,"sha256").hexdigest()!="b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82":
                raise ValueError("unapproved Xray test asset")
        with zipfile.ZipFile(xray_zip) as z: binary = Path(z.extract("xray",work))
        binary.chmod(0o700)
        xport = freeport(); client = str(uuid.uuid4())
        xc = {"inbounds":[{"listen":host,"port":xport,"protocol":"vless","settings":{"clients":[{"id":client}],"decryption":"none"}}],"outbounds":[{"protocol":"blackhole","tag":"deny"},{"protocol":"freedom","tag":"echo"}],"routing":{"rules":[{"ip":[echo_ip],"port":str(echo),"outboundTag":"echo"}]},"log":{"loglevel":"none"}}
        xc["outbounds"][1]["settings"] = {"finalRules":[
            {"action":"allow","ip":[echo_ip + "/32"],"port":str(echo)},
            {"action":"block"}]}
        xr=process(binary,["run","-config"],xc,"xray-server")
        lock = threading.Lock()
        ctl = secrets.token_urlsafe(32)
        nic = next(row["ifname"] for row in json.loads(subprocess.check_output(["ip","-j","-4","addr"]))
                   if any(a["local"] == host for a in row["addr_info"]))
        shape = UdpShape(nic, host, port, work / "tc.jsonl")
        stack.callback(shape.close)
        capture = IdleCapture(nic, host, port, access, work)
        stack.callback(capture.close)
        has_drop=False
        def remove_drop():
            nonlocal has_drop
            if has_drop:
                cmd("nft","delete","table","inet",TABLE)
                has_drop=False
        stack.callback(remove_drop)
        def switch(op):
            nonlocal has_drop
            shape.change(op)
            if op!="drop":remove_drop();return
            if has_drop:return
            batch=[f"add table inet {TABLE}"]
            for chain in ("input","output"):
                batch.append(f"add chain inet {TABLE} {chain} {{ type filter hook {chain} priority -5; policy accept; }}")
            batch += [f"add rule inet {TABLE} input ip daddr {host} udp dport {port} counter drop",
                      f"add rule inet {TABLE} output ip saddr {host} udp sport {port} counter drop"]
            subprocess.run(["nft","-f","-"],input="\n".join(batch)+"\n",text=True,check=True)
            has_drop=True
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
                        elif words[1:] == ["soak-start"]:samples.start()
                        elif words[1:] == ["soak-stop"]:samples.stop()
                        elif len(words)==3 and words[1]=="revoke" and re.fullmatch("[0-9a-f]{32}",words[2]):ledger("revoke",words[2])
                        elif len(words)==3 and words[1]=="extend" and re.fullmatch("[0-9a-f]{32}",words[2]):
                            for device in map(json.loads, ledger("devices").splitlines()):
                                if device["id"] == words[2]:
                                    until = datetime.datetime.fromtimestamp(device["expires_at"] + 86400, datetime.timezone.utc)
                                    ledger("renew", device["user_id"], until.isoformat())
                                    updated = next(row for row in map(json.loads, ledger("devices").splitlines())
                                                   if row["id"] == device["id"])
                                    self.wfile.write(b"\x01" + json.dumps(updated).encode() + b"\n")
                                    return
                            else:return
                        else:return
                    self.wfile.write(b"\x01")
                except (ValueError,OSError,subprocess.SubprocessError):return
        cport=listener(TCP,(host,0),Control)
        if installed_port is None:
            invite=ledger("invite",user,expires)
            link="nexus://invite/v2#"+b64url(json.dumps(dict(v=2,host="nexus.test",port=port,invite=invite,name="NexusLab V2")).encode())
            ca=(keys/"ca.pem").read_text()
        else:
            link=ledger("invite","--link",user,expires)
            ca=""
        sub=f"vless://{client}@{host}:{xport}?encryption=none&security=none&type=tcp#M3-Lab"
        for peer in (port,xport):
            for _ in range(100):
                if (node_process and node_process.poll() is not None) or xr.poll() is not None:raise RuntimeError("lab process exited")
                try:
                    with socket.create_connection((host,peer),.15):break
                except OSError:time.sleep(.1)
            else:raise RuntimeError("lab listeners not ready")
        (work/"device.json").write_text(json.dumps(dict(invitation=link,endpoint=f"{host}:{port}",host=host,ca=ca,echoPort=echo,target=echo_ip,controlPort=cport,control=ctl,subscription=sub)))
        print("M3 fixture ready",flush=True)
        while not (work/"stop").is_file():
            if (node_process and node_process.poll()!=None) or xr.poll()!=None:raise RuntimeError("lab process exited")
            time.sleep(.5)

if __name__ == "__main__":
    if sys.argv[1:2]==["issue"]:
        p=argparse.ArgumentParser(description=issue.__doc__)
        p.add_argument("node",type=Path);p.add_argument("work",type=Path);p.add_argument("name")
        a=p.parse_args(sys.argv[2:]);issue(a.node.resolve(),a.work.resolve(),a.name)
        raise SystemExit(0)
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("node",type=Path)
    p.add_argument("xray_zip",type=Path)
    p.add_argument("host")
    p.add_argument("work",type=Path)
    p.add_argument("--installed-ip-port",type=int,help="use the separately installed IP-mode test node on this port")
    p.add_argument("--echo-ip",default=TARGET,help="distinct TEST-NET-1 echo address for parallel fixtures")
    a=p.parse_args()
    serve(a.node.resolve(),a.xray_zip.resolve(),a.host,a.work.resolve(),a.installed_ip_port,a.echo_ip)
