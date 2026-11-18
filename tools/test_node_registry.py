"""Exercise two distinct device keys, one-time enrollment and live registry enforcement."""
import argparse
import base64
import json
import secrets
import socket
import socketserver
import tempfile
import threading
import time
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
from node_admin import Registry, b64, initialize, migration_snapshot, openssl
from test_tunnel import endpoint, process

def keypair(root, name):
    key = root/(name+".key")
    openssl("genpkey","-algorithm","EC","-pkeyopt","ec_paramgen_curve:P-256","-out",key)
    openssl("pkcs8","-topk8","-nocrypt","-in",key,"-outform","DER","-out",root/(name+".key.der"))
    return openssl("pkey","-in",key,"-pubout","-outform","DER")

def expect_denied(operation):
    try:
        operation()
    except ValueError:
        return
    raise AssertionError("operation unexpectedly allowed")

class Echo(socketserver.BaseRequestHandler):
    def handle(self):
        while data := self.request.recv(4096):
            self.request.sendall(data)

def test(binaries):
    now = int(time.time())
    with tempfile.TemporaryDirectory() as temp, socketserver.ThreadingTCPServer(("127.0.0.1",0), Echo) as echo:
        root = Path(temp)
        initialize(root/"node",dict(host="127.0.0.1",dns="1.1.1.1",port=28443,claim_port=28444,name="QA"))
        node = Registry(root/"node")
        a,b = keypair(root,"owner"),keypair(root,"friend")
        owner = node.owner("owner",dict(name="Owner",pub=b64(a)))
        (root/"owner.der").write_bytes(base64.b64decode(owner["device_der"]))
        invite = node.owner("invite",dict(name="Friend",valid_seconds=60,device_seconds=600,quota=128))["link"].split("/")[-1]
        token = json.loads(base64.urlsafe_b64decode(invite+"="*(-len(invite)%4)))["token"]
        def request(name,pub):
            proof = openssl("dgst","-sha256","-sign",root/(name+".key"),data=b"Nexus claim v1\x00"+token.encode()+pub)
            return dict(token=token,name=name,pub=b64(pub),proof=b64(proof))
        req = request("friend",b)
        with ThreadPoolExecutor(2) as pool:
            one,two = list(pool.map(node.claim,[req,req]))
        assert one==two
        expect_denied(lambda: node.claim(request("owner",a)))
        (root/"friend.der").write_bytes(base64.b64decode(one["device_der"]))
        ident = next(r["id"] for r in node.owner("devices",{}) if r["name"]=="friend")
        target = endpoint(echo.server_address[1])
        threading.Thread(target=echo.serve_forever,daemon=True).start()
        with process([binaries/"nexus-server",endpoint(0),node.root/"ca.der",node.root/"server.der",
                      node.root/"server.key.der","registry:"+str(node.root/"devices.db"),target],root) as port:
            def forward(name):
                return process([binaries/"nexus-client","tls",endpoint(port),"nexus.node",node.root/"ca.der",
                    root/(name+".der"),root/(name+".key.der"),"tcp",target,endpoint(0)],root)
            def exchange(port, data):
                with socket.create_connection(("127.0.0.1",port),timeout=5) as s:
                    s.sendall(data)
                    return s.recv(len(data))
            def denied(port):
                try:
                    assert exchange(port,b"denied")==b""
                except ConnectionResetError:
                    pass
            with forward("friend") as guest, forward("owner") as own:
                payload = secrets.token_bytes(64)
                assert exchange(guest,payload)==payload
                denied(guest)
                assert next(r["used"] for r in node.owner("devices",{}) if r["id"]==ident)==128
                node.owner("edit",dict(id=ident,expires=now+600,quota=None))
                with socket.create_connection(("127.0.0.1",guest),timeout=5) as active:
                    active.sendall(b"x"); assert active.recv(1)==b"x"
                    node.owner("revoke",dict(id=ident))
                    assert active.recv(1)==b""
                denied(guest)
                assert exchange(own,b"owner")==b"owner"
        assert any(row["action"]=="revoke" for row in node.owner("audit",{}))
        revoked = node.owner("devices",{})
        node.owner("backup",{})
        node.owner("restore",{})
        assert node.owner("devices",{})==revoked and any(r["action"]=="restore" for r in node.owner("audit",{}))
        snapshot = migration_snapshot(root/"node")
        initialize(root/"moved",dict(host="127.0.0.2",dns="1.1.1.1",port=28443,claim_port=28444,name="Moved",migration=snapshot))
        moved = Registry(root/"moved")
        assert (root/"node"/"ca.der").read_bytes() == (root/"moved"/"ca.der").read_bytes()
        assert moved.owner("devices",{}) == revoked and moved.owner("audit",{}) == node.owner("audit",{})
        assert "127.0.0.2" in moved.config["claim_url"]
        echo.shutdown()
        print("PASS: two devices, idempotent claim, redemption limit, byte quota, live revoke, owner continuity, audit")

if __name__=="__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--bin-dir",type=Path,required=True)
    test(parser.parse_args().bin_dir.resolve())
