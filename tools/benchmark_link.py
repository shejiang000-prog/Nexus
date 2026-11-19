"""Measure the two Nexus carriers over an isolated Linux veth link. Run with unshare -n."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from collections import deque
from contextlib import ExitStack
import csv
import hashlib
import http.client
import json
from http.server import ThreadingHTTPServer
import math
import os
import secrets
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import uuid
from lab_identity import generate
from test_tunnel import EchoHttp, process

def run(*args):
    subprocess.run(list(map(str,args)),check=True,stdout=subprocess.DEVNULL)

def reference_peers(stack,binary,peer,identity,target):
    password=secrets.token_urlsafe(24)
    pin=hashlib.sha256((identity/"server.der").read_bytes()).hexdigest()
    client,server,ports=[],[],{}
    for i,name in enumerate(("hysteria2","tuic","shadowsocks")):
        remote=f"192.0.2.2:{31000+i}"
        ports[name]=32000+i
        protocol={"type":name,"password":password}
        if name=="tuic": protocol["uuid"]=str(uuid.uuid4())
        if name=="shadowsocks": protocol["cipher"]="aes-128-gcm"
        node=dict(address=remote,protocol=protocol)
        listener=dict(node,rules=[dict(mask="192.0.2.5:80",action="allow",override_address=target,client_chain="direct")])
        if name!="shadowsocks":
            listener.update(transport="quic",quic_settings=dict(cert="server.pem",key="server.key",alpn_protocols=["h3"]))
            node.update(quic_settings=dict(sni_hostname="nexus.test",server_fingerprints=[pin],verify=False,alpn_protocols=["h3"]))
        server.append(listener)
        client.append(dict(address=f"127.0.0.1:{ports[name]}",protocol={"type":"mixed"},
                           rules=[dict(mask="192.0.2.5:80",action="allow",client_chain=node)]))
    for name,cfg,prefix in [("server",server,peer),("client",client,[])]:
        file=identity/(name+".yaml")
        file.write_text(json.dumps(cfg))
        proc=subprocess.Popen(prefix+[binary,"-t","2",file],cwd=identity,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        stack.callback(proc.wait,5)
        stack.callback(proc.terminate)
        time.sleep(.4)
        if proc.poll() is not None: raise RuntimeError("reference "+name+" startup failed")
    return ports

def benchmark(args):
    devices=json.loads(subprocess.check_output(["ip","-j","link","show"]))
    if os.geteuid()!=0 or os.readlink("/proc/self/ns/net")==os.readlink("/proc/1/ns/net") or [d["ifname"] for d in devices]!=["lo"]:
        raise ValueError("use a new empty network namespace: unshare -n -- python3 ...")
    if not 1<=args.count<=1000 or not 1<=args.parallel<=32 or not 1<=args.size<=512*1024:
        raise ValueError("invalid sample bounds")
    binary=args.bin_dir.resolve()
    payload=bytes(i%251 for i in range(args.size))
    with tempfile.TemporaryDirectory() as temp, ExitStack() as stack:
        identity=Path(temp)/"keys"
        generate(identity)
        child=subprocess.Popen(["unshare","-n","--","sleep","infinity"])
        stack.callback(child.wait,5)
        stack.callback(child.terminate)
        for _ in range(100):
            if os.readlink(f"/proc/{child.pid}/ns/net")!=os.readlink("/proc/self/ns/net"):
                break
            time.sleep(.02)
        else:
            raise RuntimeError("peer namespace unavailable")
        peer=["nsenter","-t",str(child.pid),"-n","--"]
        run("ip","link","set","lo","up")
        run("ip","link","add","nxl0","type","veth","peer","name","nxr0")
        run("ip","link","set","nxr0","netns",child.pid)
        for prefix,dev,addr in [([],"nxl0","192.0.2.1/30"),(peer,"nxr0","192.0.2.2/30")]:
            run(*prefix,"ip","addr","add",addr,"dev",dev)
            run(*prefix,"ip","link","set",dev,"up")
        run(*peer,"ip","link","set","lo","up")
        if args.udp:
            port=stack.enter_context(process(peer+[sys.executable,Path(__file__).resolve(),"--udp-echo"],identity))
            target=f"127.0.0.1:{port}"
            with args.output.open("x", newline="") as out:
                writer=csv.writer(out)
                writer.writerow(["version","rtt_ms","loss_percent_each_direction","association","sequence","packet_ms","since_open_ms","result"])
                for delay in args.rtt:
                    for loss in args.loss:
                        for prefix,dev in [([],"nxl0"),(peer,"nxr0")]:
                            run(*prefix,"tc","qdisc","replace","dev",dev,"root","netem","delay",f"{delay/2}ms","loss",f"{loss}%")
                        for version,folder in [("baseline",args.baseline_dir.resolve()),("mux2",binary)]:
                            with process(peer+[folder/"nexus-server","192.0.2.2:0","ca.der","server.der","server.key.der","device.der",target],identity) as server:
                                rows=subprocess.check_output([folder/"examples/udp_associations",f"192.0.2.2:{server}",target,
                                    str(args.parallel),str(args.count),"bench"],cwd=identity,text=True,timeout=180).splitlines()
                            assert len(rows)==args.parallel*args.count
                            for row in csv.reader(rows):writer.writerow((version,delay,loss,*row))
                            out.flush()
                            print(version,delay,loss,len(rows),"samples",flush=True)
            return
        port=stack.enter_context(process(peer+[sys.executable,Path(__file__).resolve(),"--echo"],identity))
        target=f"127.0.0.1:{port}"
        server=stack.enter_context(process(peer+[binary/"nexus-server","192.0.2.2:0","ca.der","server.der","server.key.der","device.der",target],identity))
        reference=reference_peers(stack,args.shoes.resolve(),peer,identity,target) if args.shoes else {}
        listeners={mode:stack.enter_context(process([binary/"nexus-client",mode,f"192.0.2.2:{server}","nexus.test",
            "ca.der","device.der","device.key.der","tcp",target,"127.0.0.1:0"],identity)) for mode in ("quic","tls")}
        listeners.update(reference)
        with args.output.open("x",newline="") as out:
            writer=csv.writer(out)
            writer.writerow(["carrier","rtt_ms","loss_percent_each_direction","parallel","payload_bytes","request","elapsed_ms","result"])
            for delay in args.rtt:
                for loss in args.loss:
                    if not 0<=delay<=1000 or not 0<=loss<=10: raise ValueError("invalid link parameters")
                    for prefix,dev in [([],"nxl0"),(peer,"nxr0")]:
                        run(*prefix,"tc","qdisc","replace","dev",dev,"root","netem","delay",f"{delay/2}ms","loss",f"{loss}%")
                    for mode,local in listeners.items():
                        def request(index):
                            start=time.monotonic()
                            conn=http.client.HTTPConnection("127.0.0.1",local,timeout=20)
                            result="ok"
                            try:
                                conn.request("POST","http://192.0.2.5/" if mode in reference else "/",body=payload)
                                response=conn.getresponse()
                                if response.status!=200 or response.read()!=payload: result="bad-response"
                            except (OSError,http.client.HTTPException) as exc: result=type(exc).__name__
                            finally: conn.close()
                            return index,round((time.monotonic()-start)*1000,2),result
                        request(-1)
                        with ThreadPoolExecutor(args.parallel) as pool: rows=list(pool.map(request,range(args.count)))
                        for row in rows: writer.writerow((mode,delay,loss,args.parallel,args.size,*row))
                        out.flush()
                        ok=sorted(row[1] for row in rows if row[2]=="ok")
                        pct=lambda q:ok[math.ceil(len(ok)*q)-1] if ok else None
                        print(mode,delay,loss,"OK",f"{len(ok)}/{len(rows)}","P50/P95/P99 ms",pct(.5),pct(.95),pct(.99),flush=True)

if __name__=="__main__":
    p=argparse.ArgumentParser()
    p.add_argument("--baseline-dir",type=Path)
    p.add_argument("--udp",action="store_true")
    p.add_argument("--udp-echo",action="store_true")
    p.add_argument("--echo",action="store_true")
    p.add_argument("--bin-dir",type=Path)
    p.add_argument("--shoes",type=Path,help="optional upstream Linux shoes binary for reference peers")
    p.add_argument("--output",type=Path)
    p.add_argument("--rtt",nargs="+",type=int,default=[20,80,180])
    p.add_argument("--loss",nargs="+",type=int,default=[0,1,3,5])
    p.add_argument("--size",type=int,default=8192)
    p.add_argument("--count",type=int,default=12)
    p.add_argument("--parallel",type=int,default=4)
    args=p.parse_args()
    if args.udp_echo:
        import socket
        with socket.socket(type=socket.SOCK_DGRAM) as sock:
            sock.bind(("127.0.0.1",0))
            print(f"READY 127.0.0.1:{sock.getsockname()[1]}",flush=True)
            while True:
                packet,peer=sock.recvfrom(2048)
                sock.sendto(packet,peer)
    elif args.echo:
        with ThreadingHTTPServer(("127.0.0.1",0),EchoHttp) as http:
            http.received=deque(maxlen=0)
            print(f"READY 127.0.0.1:{http.server_port}",flush=True)
            http.serve_forever()
    else: benchmark(args)
