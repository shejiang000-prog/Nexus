"""Port-scoped NexusLab link shaping and metadata-only idle traffic measurement."""
import collections
import contextlib
import json
import secrets
import socket
import struct
import subprocess
import threading
import time


class UdpShape:
    def __init__(self, nic, host, port, stats):
        self.nic, self.host, self.port = nic, host, str(port)
        self.stats = stats
        self.resources = None

    def close(self):
        if self.resources is not None:
            self.resources.close()
            self.resources = None

    def change(self, mode):
        self.close()
        if mode not in ("loss", "rate"):
            return
        cmd = lambda *args: subprocess.run(args, check=True, stdout=subprocess.DEVNULL)
        dev = "nm3" + secrets.token_hex(3)
        with contextlib.ExitStack() as stack:
            cmd("ip","link","add",dev,"type","ifb")
            stack.callback(cmd,"ip","link","del",dev)
            cmd("ip","link","set",dev,"up")
            # Redirect only this disposable node's outgoing UDP into a private queue.
            policy = ("loss","20%") if mode == "loss" else ("rate","2mbit")
            cmd("tc","qdisc","add","dev",dev,"root","netem",*policy)
            qdiscs = json.loads(subprocess.check_output(["tc","-j","qdisc","show","dev",self.nic]))
            if not any(q["kind"] == "clsact" for q in qdiscs):
                cmd("tc","qdisc","add","dev",self.nic,"clsact")
                def remove_empty():
                    if all(not json.loads(subprocess.check_output(["tc","-j","filter","show","dev",self.nic,direction]))
                           for direction in ("ingress","egress")):
                        cmd("tc","qdisc","del","dev",self.nic,"clsact")
                stack.callback(remove_empty)
            filters = json.loads(subprocess.check_output(["tc","-j","filter","show","dev",self.nic,"egress"]))
            pref = str(next(p for p in range(17000,18000) if all(f.get("pref") != p for f in filters)))
            select = ["dev",self.nic,"egress","protocol","ip","pref",pref]
            cmd("tc","filter","add",*select,"flower","ip_proto","udp","src_ip",self.host,"src_port",self.port,
                "action","mirred","egress","redirect","dev",dev)
            stack.callback(cmd,"tc","filter","del",*select)
            def save_counts():
                q = json.loads(subprocess.check_output(["tc","-j","-s","qdisc","show","dev",dev]))[0]
                with self.stats.open("a") as out:
                    print(json.dumps({"mode":mode,**{k:q[k] for k in ("packets","bytes","drops")}}),file=out)
            stack.callback(save_counts)
            self.resources = stack.pop_all()


class IdleCapture:
    def __init__(self, nic, host, port, access, work):
        self.nic, self.host, self.port = nic, socket.inet_aton(host), port
        self.access, self.work = access, work
        self.sock = None

    def start(self, kind):
        if self.sock is not None: raise ValueError("capture already active")
        self.kind = kind
        self.events = []
        self.offset = self.access.stat().st_size if self.access.exists() else 0
        # Linux packet socket provides just the IPv4 headers needed for times, direction and counts.
        self.sock = socket.socket(socket.AF_PACKET,socket.SOCK_DGRAM,socket.htons(0x0800))
        self.sock.bind((self.nic,0x0800))
        self.sock.settimeout(.5)
        self.started = time.monotonic()
        self.done = threading.Event()
        def collect():
            while not self.done.is_set():
                try: packet = self.sock.recv(80)
                except socket.timeout: continue
                ihl = (packet[0] & 15)*4
                if len(packet) < ihl+14 or packet[16:20] != self.host or packet[9] not in (6,17): continue
                if struct.unpack_from("!H",packet,ihl+2)[0] != self.port: continue
                if packet[9] == 17: proto = "udp"
                else:
                    payload = struct.unpack_from("!H",packet,2)[0] - ihl - (packet[ihl+12] >> 4)*4
                    proto = "tcp-data" if payload else "tcp-control"
                self.events.append((round(time.monotonic()-self.started,3),proto))
        self.reader = threading.Thread(target=collect,daemon=True)
        self.reader.start()

    def close(self):
        if self.sock is not None:
            self.done.set()
            self.reader.join()
            self.sock.close()
            self.sock = None

    def finish(self):
        if self.sock is None: raise ValueError("no capture active")
        duration = time.monotonic()-self.started
        self.close()
        methods = collections.Counter()
        if self.access.exists():
            with self.access.open("rb") as log:
                log.seek(self.offset)
                for line in log: methods[json.loads(line)["request"]["method"]] += 1
        out = {"seconds":round(duration,3),"packets":dict(collections.Counter(p for _,p in self.events)),
               "methods":dict(methods),"client_packet_seconds":[t for t,_ in self.events]}
        (self.work / ("idle-" + self.kind + ".json")).write_text(json.dumps(out))
        packet_limit = 2 if self.kind == "h2" else 0
        if duration < 600 or len(self.events) > packet_limit or methods:
            raise ValueError(f"{self.kind} idle capture failed: see idle-{self.kind}.json")
