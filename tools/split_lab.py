"""Scoped DNS and TCP/UDP receipt fixtures for Android split-routing acceptance (Linux)."""
import contextlib
import ipaddress
import json
import socket
import socketserver
import struct
import subprocess
import threading
from pathlib import Path

PROXY = "192.0.2.45"

def serve(args, root, nodes, inbounds, shoes):
    assert ipaddress.ip_address(args.host).is_private and args.host != PROXY
    with contextlib.ExitStack() as stack:
        def command(*cmd):
            subprocess.run(cmd, check=True)
        command("ip","addr","add",PROXY+"/32","dev","lo")
        stack.callback(command,"ip","addr","del",PROXY+"/32","dev","lo")
        def listener(cls, addr, handler):
            server = stack.enter_context(cls(addr, handler))
            threading.Thread(target=server.serve_forever,daemon=True).start()
            stack.callback(server.shutdown)
            return server.server_address[1]

        def path(handler):
            host, port = handler.client_address
            if host not in {args.host, PROXY, "127.0.0.1"}:
                return "direct"
            # Corroborate the target's peer address with the sending socket's Linux owner.
            udp = isinstance(handler.server, socketserver.UDPServer)
            sockets = subprocess.check_output(
                ["ss", "-Hnaup" if udp else "-Hnatp", "sport", "=", f":{port}"], text=True)
            for engine in ("nexus-server", "xray", "shoes"):
                if f'"{engine}",' in sockets:
                    print("NODE", engine, "UDP" if udp else "TCP", flush=True)
                    return "node"
            return "unverified"

        class Tcp(socketserver.StreamRequestHandler):
            def handle(self):
                self.request.settimeout(8)
                request=self.rfile.readline(64)
                if request in (b"GET /down HTTP/1.1\r\n", b"POST /up HTTP/1.1\r\n"):
                    for _ in range(12):
                        if self.rfile.readline(256)==b"\r\n": break
                    else: return
                    down=request.startswith(b"GET")
                    if not down and len(self.rfile.read(1048576))!=1048576: return
                    body=bytes(1048576) if down else b"ok"
                    self.wfile.write(b"HTTP/1.1 200 OK\r\nContent-Length: "+str(len(body)).encode()+
                                     b"\r\nConnection: close\r\n\r\n"+body)
                elif request == b"receipt\n":
                    via = path(self)
                    print("TCP",via,flush=True)
                    self.wfile.write(via.encode()+b"\n")

        class Udp(socketserver.BaseRequestHandler):
            def handle(self):
                data, sock = self.request
                if data == b"receipt":
                    via = path(self)
                    print("UDP",via,flush=True)
                    sock.sendto(via.encode(),self.client_address)

        def dns(handler, request):
            end = request.find(b"\0",12)+5
            if end < 17 or end > len(request): return b""
            name = request[12:end-5]
            if name not in {b"\x05local\x05split\x04test",b"\x06remote\x05split\x04test"}: return b""
            local = name.startswith(b"\x05")
            domestic = handler.server.server_address[0] == args.host
            print("DNS", "domestic" if domestic else "node-resolver", "local" if local else "remote", path(handler),flush=True)
            if domestic and not local: return b""
            return (request[:2]+struct.pack("!5H",0x8180,1,1,0,0)+request[12:end]+
                    b"\xc0\x0c"+struct.pack("!HHIH",1,1,45,4)+socket.inet_aton(args.host if domestic else PROXY))
        class DnsUdp(socketserver.BaseRequestHandler):
            def handle(self):
                packet,sock=self.request
                sock.sendto(dns(self,packet),self.client_address)
        class DnsTcp(socketserver.StreamRequestHandler):
            def handle(self):
                prefix=self.rfile.read(2)
                if len(prefix)==2:
                    reply=dns(self,self.rfile.read(int.from_bytes(prefix,"big")))
                    self.wfile.write(struct.pack("!H",len(reply))+reply)

        port=listener(socketserver.ThreadingTCPServer,(args.host,0),Tcp)
        for ip in (PROXY,args.host):
            if ip==PROXY: listener(socketserver.ThreadingTCPServer,(ip,port),Tcp)
            listener(socketserver.ThreadingUDPServer,(ip,port),Udp)
        direct_dns=listener(socketserver.ThreadingUDPServer,(args.host,0),DnsUdp)
        listener(socketserver.ThreadingTCPServer,(args.host,direct_dns),DnsTcp)
        listener(socketserver.ThreadingUDPServer,(PROXY,53),DnsUdp)
        listener(socketserver.ThreadingTCPServer,(PROXY,53),DnsTcp)
        # A peer can connect only to the two lab receipt targets and node-side DNS.
        allowed=[(args.host,port),(PROXY,port),(PROXY,53)]
        final = [dict(action="allow",ip=[ip],port=str(p)) for ip,p in allowed] + [dict(action="block")]
        out=[dict(protocol="freedom",tag="receipt",settings=dict(finalRules=final)),dict(protocol="blackhole",tag="deny")]
        rules=[dict(ip=[ip],port=str(p),outboundTag="receipt") for ip,p in allowed]
        rules.append(dict(network="tcp,udp",outboundTag="deny"))
        cfg={"inbounds":[inbounds[0]],"outbounds":out,"routing":{"rules":rules},"log":{"loglevel":"none"}}
        shoe=shoes[0].copy()
        shoe["rules"]=[dict(mask=f"{ip}:{p}",action="allow",client_chain="direct") for ip,p in allowed]+[dict(masks=["0.0.0.0/0","::/0"],action="block")]
        for config, binary, name in ((cfg,[args.xray,"run","-config"],"xray"),([shoe],[args.shoes,"-t","2","--no-reload"],"shoes")):
            file=root/(name+"-split.json")
            file.write_text(json.dumps(config))
            proc=subprocess.Popen(binary+[file],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            def stop(p):
                p.terminate()
                p.wait(8)
            stack.callback(stop,proc)
        with args.fixture.open("x") as f:
            json.dump(dict(format="nexus-nodes/1", nodes=[n for n in nodes if n["type"] in ("tuic","vless")],
                           host=args.host,proxy=PROXY,port=port,domesticPort=direct_dns),f)
        stack.callback(args.fixture.unlink,missing_ok=True)
        print("split fixtures ready",flush=True)
        input()
