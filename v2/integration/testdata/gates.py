"""Run in a fresh lab netns with round-one tools on PYTHONPATH."""
import argparse
import csv
import json
import os
import sqlite3
import ssl
import sys
import tempfile
import subprocess
import socket
import time
from pathlib import Path
from contextlib import ExitStack
import benchmark_v2_official as bench


if sys.argv[1:]==["udp-origin"]:
    with socket.socket(type=socket.SOCK_DGRAM) as sock:
        sock.bind(("127.0.0.1",32554))
        while True:
            data,peer=sock.recvfrom(65535)
            sock.sendto(data,peer)

p=argparse.ArgumentParser(description=__doc__)
p.add_argument("binary", type=Path)
p.add_argument("tools", type=Path, help="directory containing the verified official hysteria binary")
p.add_argument("output", type=Path)
p.add_argument("--loss", type=int, required=True, choices=(0, 1, 2, 5))
p.add_argument("--seed", type=int, default=1907)
p.add_argument("--pairs", type=int, default=20)
p.add_argument("--competitor", choices=("hy-default","box-tuic"), default="hy-default")
p.add_argument("--udp", choices=("capsule","datagram"))
p.add_argument("--quota", action="store_true", help="measure with a 1 TiB synthetic account")
a=p.parse_args()
if a.pairs < 1:
    p.error("positive pair count required")

with ExitStack() as stack:
    peer=bench.network(stack)
    keys=Path(stack.enter_context(tempfile.TemporaryDirectory()))/"keys"
    bench.generate(keys)
    if a.quota:
        db = keys / "quota.db"
        admin = [os.environ["LAB_NODE"], "ledger", db]
        user = subprocess.check_output([*admin, "create-user"], text=True).strip()
        subprocess.run([*admin, "set-quota", user, str(1 << 40)], check=True)
        public = bytes.fromhex(subprocess.check_output([a.binary, "public-key"], cwd=keys, text=True))
        # Seed the disposable device before timing. The gate client uses the all-zero lab ID.
        with sqlite3.connect(db) as con:
            con.execute("PRAGMA foreign_keys=ON")
            con.execute("INSERT INTO invites VALUES(zeroblob(32),?,?,0)", (bytes.fromhex(user),int(time.time())+86400))
            con.execute("INSERT INTO devices VALUES(zeroblob(16),zeroblob(32),zeroblob(16),?,'lab',NULL)", (public,))
        con.close()
        os.environ["LAB_QUOTA_DB"] = str(db)
        def used():
            account = json.loads(subprocess.check_output([*admin, "users"]))
            if account["quota_bytes"] != 1 << 40:
                raise ValueError("lab quota mismatch")
            return account["used_bytes"]
    if a.udp:
        os.environ["LAB_CAPSULES"]="1" if a.udp=="capsule" else "0"
        bench.shape(peer,100,a.loss,20,a.seed)
        stack.enter_context(bench.child([*peer,sys.executable,Path(__file__).resolve(),"udp-origin"],keys))
        stack.enter_context(bench.child([*peer,a.binary,"server"],keys))
        time.sleep(.35)
        with a.output.open("x") as f:
            print("seq,rtt_ms",file=f,flush=True)
            subprocess.run([a.binary,"udp"],cwd=keys,stdout=f,timeout=45,check=True)
    else:
        for prefix,dev in (([],"v2c"),(peer,"v2s")):
            bench.run(*prefix,"ip","link","set","dev",dev,"gso_max_size",1500,"gro_max_size",1500)
        stack.enter_context(bench.process([*peer,sys.executable,bench.HERE,"--origin"],keys))
        ctx=ssl.create_default_context(cafile=str(keys/"ca.pem"))
        f=stack.enter_context(a.output.open("x",newline=""))
        w=csv.writer(f,lineterminator="\n")
        w.writerow(("loss","pair","mode","status","mbps"))
        for i in range(a.pairs):
            for mode in ("go-bbr",a.competitor):
                bench.shape(peer,150,a.loss,20,a.seed+i*2)
                if mode=="go-bbr":
                    server,client=[*peer,a.binary,"server"],[a.binary,"client"]
                    route="v1-quic"  # benchmark helper's raw local TCP forward, no SOCKS preface
                else:
                    server,client=bench.configure(mode,a,peer,keys,20)
                    route=mode
                try:
                    if a.quota and mode=="go-bbr":
                        before=used()
                    with ExitStack() as t:
                        srv=t.enter_context(bench.child(server,keys))
                        time.sleep(.35)
                        if srv.poll() is not None:
                            raise RuntimeError("server exited")
                        proc=t.enter_context(bench.child(client,keys))
                        bench.ready(proc)
                        with bench.deadline(60),bench.connect(route,ctx) as tls:
                            bench.request(tls,"small",1)
                            bench.request(tls,"small",1)
                        with bench.deadline(60),bench.connect(route,ctx) as tls:
                            _,sec=bench.request(tls,"bulk",bench.SIZE)
                    if a.quota and mode=="go-bbr" and used()-before < bench.SIZE:
                        raise ValueError("missing quota settlement")
                    w.writerow((a.loss,i,mode,"ok",round(8*bench.SIZE/sec/1e6,4)))
                except (OSError,ValueError,RuntimeError,EOFError) as e:
                    w.writerow((a.loss,i,mode,type(e).__name__,""))
                f.flush()
