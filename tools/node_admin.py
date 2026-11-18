"""Private node registry. Owner commands run locally over SSH, with JSON on stdin."""
import argparse
import base64
from functools import lru_cache
from contextlib import closing
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import ssl
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

@lru_cache(maxsize=2)
def can_backdate(command):
    output = subprocess.run(["openssl",command,"-help"],capture_output=True,timeout=5,check=True)
    return b"-not_before " in output.stderr + output.stdout

def clock_margin(command):
    # Debian 13 supports an explicit start time. Older OpenSSL is used by CI.
    return ["-not_before",time.strftime("%Y%m%d%H%M%SZ",time.gmtime(time.time()-120))] if can_backdate(command) else []

SCHEMA = """
CREATE TABLE IF NOT EXISTS invites(hash BLOB PRIMARY KEY, label TEXT NOT NULL, claim_until INTEGER NOT NULL,
    expires INTEGER NOT NULL, quota INTEGER, uses INTEGER NOT NULL, max_devices INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS devices(cert BLOB PRIMARY KEY, id TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
    invite BLOB, pub BLOB NOT NULL, expires INTEGER NOT NULL, quota INTEGER, used INTEGER NOT NULL DEFAULT 0,
    revoked INTEGER NOT NULL DEFAULT 0, UNIQUE(invite,pub));
CREATE TABLE IF NOT EXISTS audit(at INTEGER NOT NULL DEFAULT(unixepoch()), action TEXT NOT NULL, subject TEXT NOT NULL);
"""
def b64(data):
    return base64.b64encode(data).decode("ascii")

def json64(value):
    return base64.urlsafe_b64encode(json.dumps(value, separators=(",", ":")).encode()).decode().rstrip("=")

def integer(value, low, high):
    if type(value) is not int or not low <= value <= high:
        raise ValueError("invalid number")
    return value

def label(value):
    if not isinstance(value, str) or not value.strip() or len(value) > 64 or not value.isprintable():
        raise ValueError("invalid label")
    return value.strip()

def openssl(*args, data=None):
    return subprocess.run(["openssl", *map(str, args)], input=data, stdout=subprocess.PIPE,
                          stderr=subprocess.DEVNULL, timeout=10, check=True).stdout

class Registry:
    def __init__(self, root):
        self.root = root.resolve()
        self.config = json.loads((self.root / "node.json").read_text())

    def connect(self):
        conn = sqlite3.connect(self.root / "devices.db", timeout=3)
        conn.row_factory = sqlite3.Row
        return conn

    def profile(self, cert):
        return dict(self.config["profile"], ca_der=b64((self.root / "ca.der").read_bytes()), device_der=b64(cert))

    def sign(self, pub):
        if len(pub) != 91 or not pub.startswith(bytes.fromhex("3059301306072a8648ce3d020106082a8648ce3d03010703420004")):
            raise ValueError("expected P-256 device key")
        openssl("pkey", "-pubin", "-inform", "DER", "-pubcheck", "-noout", data=pub)
        with tempfile.TemporaryDirectory() as tmp:
            key = Path(tmp)/"public.pem"
            key.write_bytes(openssl("pkey", "-pubin", "-inform", "DER", data=pub))
            ext = Path(tmp)/"client.ext"
            ext.write_text("basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=clientAuth\n")
            return openssl("x509", "-new", *clock_margin("x509"), "-force_pubkey", key, "-subj", "/CN=Nexus device", "-CA", self.root/"ca.pem",
                    "-CAkey", self.root/"ca.key", "-set_serial", "0x"+secrets.token_hex(16), "-days", "365",
                    "-extfile", ext, "-outform", "DER")

    def issue(self, conn, pub, name, expires, quota, invite=None):
        cert = self.sign(pub)
        device = secrets.token_hex(12)
        conn.execute("INSERT INTO devices(cert,id,name,invite,pub,expires,quota) VALUES (?,?,?,?,?,?,?)",
                     (cert, device, label(name), invite, pub, expires, quota))
        conn.execute("INSERT INTO audit(action,subject) VALUES ('issued',?)", (device,))
        return self.profile(cert)

    def owner(self, action, args):
        if action in ("backup","restore"):
            file = self.root/"authorization-backup.sqlite"
            pin = hashlib.sha256((self.root/"ca.der").read_bytes()).hexdigest()
            if action=="backup":
                with tempfile.TemporaryDirectory(dir=self.root) as directory:
                    tmp = Path(directory)/"snapshot"
                    with closing(self.connect()) as live, closing(sqlite3.connect(tmp)) as dest:
                        live.backup(dest)
                        dest.execute("PRAGMA journal_mode=DELETE")
                        dest.execute("CREATE TABLE IF NOT EXISTS nexus_snapshot(pin TEXT NOT NULL, config TEXT NOT NULL)")
                        dest.execute("DELETE FROM nexus_snapshot")
                        dest.execute("INSERT INTO nexus_snapshot VALUES (?,?)",(pin,json.dumps(self.config,sort_keys=True)))
                        dest.commit()
                    os.replace(tmp,file)
            else:
                with closing(sqlite3.connect(file.as_uri()+"?mode=ro",uri=True)) as source, closing(self.connect()) as live:
                    meta = source.execute("SELECT pin,config FROM nexus_snapshot").fetchone()
                    if meta!=(pin,json.dumps(self.config,sort_keys=True)) or source.execute("PRAGMA integrity_check").fetchone()!=("ok",):
                        raise ValueError("backup does not match this installation")
                    source.backup(live)
                    live.execute("PRAGMA journal_mode=WAL")
                    live.execute("INSERT INTO audit(action,subject) VALUES ('restore','owner')")
                    live.commit()
            return {"ok":True}
        now = int(time.time())
        with self.connect() as conn:
            conn.execute("BEGIN IMMEDIATE")
            if action == "invite":
                seconds = integer(args["valid_seconds"], 1, 7*86400)
                expires = now + integer(args["device_seconds"], seconds, 365*86400)
                uses = integer(args.get("uses", 1), 1, 128)
                devices = integer(args.get("max_devices", uses), 1, 128)
                quota = integer(args["quota"], 1, 2**60) if args.get("quota") is not None else None
                token = secrets.token_urlsafe(32)
                digest = hashlib.sha256(token.encode()).digest()
                conn.execute("INSERT INTO invites VALUES (?,?,?,?,?,?,?)",
                             (digest, label(args["name"]), now+seconds, expires, quota, uses, devices))
                conn.execute("INSERT INTO audit(action,subject) VALUES ('invite',?)", (digest.hex()[:16],))
                info = {"v":1, "name":args["name"], "url":self.config["claim_url"], "token":token,
                        "pin":hashlib.sha256((self.root/"server.der").read_bytes()).hexdigest()}
                return {"link":"nexus://invite/"+json64(info)}
            if action in ("devices","audit"):
                query = ("SELECT id,name,expires,quota,used,revoked FROM devices ORDER BY rowid DESC" if action=="devices"
                         else "SELECT at,action,subject FROM audit ORDER BY rowid DESC LIMIT 200")
                return [dict(row) for row in conn.execute(query)]
            if action in ("revoke","edit"):
                ident = args["id"]
                if action == "revoke":
                    changed = conn.execute("UPDATE devices SET revoked=1 WHERE id=?", (ident,))
                else:
                    expires = integer(args["expires"], now+1, now+365*86400)
                    quota = integer(args["quota"], 1, 2**60) if args.get("quota") is not None else None
                    changed = conn.execute("UPDATE devices SET expires=?,quota=? WHERE id=? AND revoked=0", (expires,quota,ident))
                if changed.rowcount != 1:
                    raise ValueError("unknown device")
                conn.execute("INSERT INTO audit(action,subject) VALUES (?,?)",(action,ident))
                return {"ok":True}
            if action == "owner":
                pub = base64.b64decode(args["pub"], validate=True)
                return self.issue(conn, pub, args["name"], now+365*86400, None)
            raise ValueError("unknown owner action")

    def claim(self, request):
        token, name = request["token"], label(request["name"])
        if not isinstance(token,str) or len(token) != 43:
            raise ValueError("invalid invitation")
        digest = hashlib.sha256(token.encode("ascii")).digest()
        pub = base64.b64decode(request["pub"],validate=True)
        signature = base64.b64decode(request["proof"],validate=True)
        now = int(time.time())
        with self.connect() as conn:
            conn.execute("BEGIN IMMEDIATE")
            invite = conn.execute("SELECT * FROM invites WHERE hash=? AND claim_until>? AND expires>?",(digest,now,now)).fetchone()
            if invite is None:
                raise ValueError("invalid invitation")
            with tempfile.TemporaryDirectory() as tmp:
                key, sig = Path(tmp)/"public.pem", Path(tmp)/"proof"
                key.write_bytes(openssl("pkey","-pubin","-inform","DER",data=pub))
                sig.write_bytes(signature)
                openssl("dgst","-sha256","-verify",key,"-signature",sig,data=b"Nexus claim v1\x00"+token.encode()+pub)
            prior = conn.execute("SELECT cert,revoked FROM devices WHERE invite=? AND pub=?",(digest,pub)).fetchone()
            if prior is not None and not prior["revoked"]:
                return self.profile(prior["cert"])
            count = conn.execute("SELECT count(*) FROM devices WHERE invite=? AND revoked=0",(digest,)).fetchone()[0]
            if prior or invite["uses"] < 1 or count >= invite["max_devices"]:
                raise ValueError("invitation unavailable")
            profile = self.issue(conn,pub,name,invite["expires"],invite["quota"],digest)
            conn.execute("UPDATE invites SET uses=uses-1 WHERE hash=?",(digest,))
            return profile

# Logical snapshots contain fixed data columns only. Restore never executes SQL from the archive.
MIGRATION_TABLES = {
    "invites": ("hash,label,claim_until,expires,quota,uses,max_devices", (0,)),
    "devices": ("cert,id,name,invite,pub,expires,quota,used,revoked", (0,3,4)),
    "audit": ("at,action,subject", ()),
}

def migration_snapshot(root):
    result = {"format":"nexus-migration/1", "ca":(root/"ca.pem").read_text(), "key":(root/"ca.key").read_text()}
    with closing(sqlite3.connect(root/"devices.db")) as conn:
        conn.execute("BEGIN")
        for table, (columns, blobs) in MIGRATION_TABLES.items():
            rows = []
            for record in conn.execute(f"SELECT {columns} FROM {table}"):
                row = list(record)
                for i in blobs:
                    if row[i] is not None: row[i] = b64(row[i])
                rows.append(row)
            result[table] = rows
    if len(json.dumps(result).encode()) > 4*1024*1024:
        raise ValueError("migration archive exceeds 4 MiB")
    return result

def restore_rows(conn, snapshot):
    for table, (columns, blobs) in MIGRATION_TABLES.items():
        names = columns.split(",")
        for values in snapshot[table]:
            if not isinstance(values,list) or len(values)!=len(names): raise ValueError("invalid migration row")
            row = list(values)
            for i in blobs:
                if row[i] is not None: row[i] = base64.b64decode(row[i],validate=True)
            conn.execute(f"INSERT INTO {table}({columns}) VALUES ({','.join('?' for _ in names)})", row)

def initialize(root, args):
    if root.exists():
        raise ValueError("choose a new directory")
    host = ipaddress.ip_address(args["host"])
    dns = ipaddress.IPv4Address(args["dns"])
    port = integer(args["port"],1,65535)
    claim_port = integer(args["claim_port"],1,65535)
    if port == claim_port:
        raise ValueError("ports must differ")
    address = f"[{host}]" if host.version==6 else str(host)
    root.mkdir(mode=0o700)
    migration = args.get("migration")
    if migration is not None:
        if migration["format"] != "nexus-migration/1": raise ValueError("invalid migration format")
        (root/"ca.key").write_text(migration["key"])
        (root/"ca.pem").write_text(migration["ca"])
        if openssl("pkey","-in",root/"ca.key","-pubout") != openssl("x509","-in",root/"ca.pem","-noout","-pubkey"):
            raise ValueError("CA key mismatch")
    else:
        openssl("req","-x509",*clock_margin("req"),"-newkey","ec","-pkeyopt","ec_paramgen_curve:P-256","-noenc","-subj","/CN=Nexus private CA",
                "-days","3650","-addext","basicConstraints=critical,CA:TRUE","-addext","keyUsage=critical,keyCertSign,cRLSign",
                "-keyout",root/"ca.key","-out",root/"ca.pem")
    openssl("req","-newkey","ec","-pkeyopt","ec_paramgen_curve:P-256","-noenc","-subj","/CN=nexus.node",
            "-keyout",root/"server.key","-out",root/"server.csr")
    ext = root/"server.ext"
    ext.write_text(f"basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:nexus.node,IP:{host}\n")
    openssl("x509","-req",*clock_margin("x509"),"-in",root/"server.csr","-CA",root/"ca.pem","-CAkey",root/"ca.key","-set_serial","0x"+secrets.token_hex(16),
            "-days","365","-extfile",ext,"-out",root/"server.pem")
    for name in ("ca","server"):
        openssl("x509","-in",root/(name+".pem"),"-outform","DER","-out",root/(name+".der"))
    openssl("pkcs8","-topk8","-nocrypt","-in",root/"server.key","-outform","DER","-out",root/"server.key.der")
    cfg = {"claim_url":f"https://{address}:{claim_port}/claim","claim_port":claim_port,
           "profile":{"version":2,"name":label(args["name"]),"server":f"{address}:{port}","tls_name":"nexus.node",
                      "target":f"{dns}:53","dns":str(dns)}}
    (root/"node.json").write_text(json.dumps(cfg))
    with sqlite3.connect(root/"devices.db") as conn:
        conn.execute("PRAGMA journal_mode=WAL")
        conn.executescript(SCHEMA)
        if migration is not None: restore_rows(conn,migration)
    return {"ok":True}

def serve(registry):
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    ctx.load_cert_chain(registry.root/"server.pem",registry.root/"server.key")
    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            try:
                size = int(self.headers["Content-Length"])
                if self.path != "/claim" or not 1 <= size <= 4096:
                    raise ValueError()
                data = json.dumps(registry.claim(json.loads(self.rfile.read(size)))).encode()
                self.send_response(200)
                self.send_header("Content-Type","application/json")
                self.send_header("Cache-Control","no-store")
                self.send_header("Content-Length",str(len(data)))
                self.end_headers()
                self.wfile.write(data)
            except Exception:
                self.send_error(403)
        def log_message(self,*args):
            pass
    class Server(ThreadingHTTPServer):
        slots = threading.BoundedSemaphore(8)
        request_window = {}
        def process_request(self, request, client_address):
            # This admission hook runs on the single accept thread, before TLS or certificate signing.
            minute = int(time.monotonic() // 60)
            ip = client_address[0]
            if len(self.request_window) >= 512 and ip not in self.request_window:
                self.request_window = {k:v for k,v in self.request_window.items() if v[0] == minute}
            window,count = self.request_window.get(ip,(minute,0))
            count = count + 1 if window == minute else 1
            if count > 30 or (ip not in self.request_window and len(self.request_window) >= 512):
                self.shutdown_request(request)
                return
            self.request_window[ip] = (minute,count)
            if not self.slots.acquire(blocking=False):
                self.shutdown_request(request)
                return
            super().process_request(request, client_address)
        def process_request_thread(self, request, addr):
            try:
                request.settimeout(5)
                with ctx.wrap_socket(request,server_side=True) as channel:
                    Handler(channel,addr,self)
            except (OSError,ValueError):
                pass
            finally:
                self.shutdown_request(request)
                self.slots.release()
    host = registry.config["profile"]["server"].rsplit(":",1)[0].strip("[]")
    Server.address_family = socket.AF_INET6 if ":" in host else socket.AF_INET
    with Server(("::" if ":" in host else "0.0.0.0",registry.config["claim_port"]),Handler) as server:
        server.serve_forever()

if __name__ == "__main__":
    import sys
    os.umask(0o077)
    parser = argparse.ArgumentParser()
    parser.add_argument("root",type=Path)
    parser.add_argument("action",choices=["init","serve","owner","invite","claim","devices","revoke","edit","audit","backup","restore","migration-export"])
    args = parser.parse_args()
    if args.action == "serve":
        serve(Registry(args.root))
    elif args.action == "migration-export":
        print(json.dumps(migration_snapshot(args.root)))
    else:
        maximum = 5*1024*1024 if args.action == "init" else 8192
        payload = sys.stdin.buffer.read(maximum+1)
        if len(payload)>maximum: raise ValueError("request too large")
        request = json.loads(payload or b"{}")
        if args.action == "init":
            result = initialize(args.root,request)
        else:
            node = Registry(args.root)
            result = node.claim(request) if args.action=="claim" else node.owner(args.action,request)
        print(json.dumps(result))
