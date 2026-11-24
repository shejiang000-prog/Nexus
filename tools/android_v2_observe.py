"""Local counters for the disposable NexusLab node and its synthetic echo service."""
import contextlib
import http.client
import json
import re
import socket
import threading
import time
from pathlib import Path


@contextlib.contextmanager
def admin(sock, method, path, obj=None):
    # No listener is exposed. The Unix socket belongs to the single fixture node.
    with socket.socket(socket.AF_UNIX) as raw:
        raw.settimeout(8)
        raw.connect(str(sock))
        conn=http.client.HTTPConnection("localhost")
        conn.sock=raw
        try:
            conn.request(method,path,json.dumps(obj).encode() if obj is not None else None,
                         {"Content-Type":"application/json"} if obj is not None else {})
            with conn.getresponse() as response:
                if response.status!=200: raise RuntimeError(f"fixture admin HTTP {response.status}")
                yield response
        finally: conn.close()


@contextlib.contextmanager
def installed_echo(sock, authority, target):
    # Refuse arbitrary pre-existing routing entries or a different installed node.
    with admin(sock,"GET","/config/") as response: config=json.load(response)
    handlers=[h for srv in config["apps"]["http"]["servers"].values()
              for r in srv["routes"] for h in r.get("handle",[])
              if h.get("handler")=="nexus" and h.get("authority")==authority]
    if len(handlers)!=1 or handlers[0].get("allow_exact"):
        raise ValueError("expected a newly installed IP-mode test node")
    handlers[0]["allow_exact"]=[target]
    with admin(sock,"POST","/load",config): pass
    try: yield
    finally:
        del handlers[0]["allow_exact"]
        with admin(sock,"POST","/load",config): pass


class NodeSamples:
    def __init__(self,pid,admin_sock,target_port,out):
        self.proc=Path("/proc")/str(pid)
        self.sock,self.port,self.out=admin_sock,target_port,out
        self.stopped=threading.Event()
        self.thread=None
        self.failure=None

    def sample(self,label):
        # Read the profile count, discard the stacks, and never sample the public HTTP endpoint.
        with admin(self.sock,"GET","/debug/pprof/goroutine?debug=1") as response:
            match=re.fullmatch(rb"goroutine profile: total (\d+)\s*",response.readline(100))
            if match is None:raise RuntimeError("node goroutine counter unavailable")
        rollup=(self.proc/"smaps_rollup").read_text()
        sizes={k.lower()+"KiB":int(v) for k,v in re.findall(r"^(Rss|Pss):\s+(\d+) kB",rollup,re.M)}
        fds=list((self.proc/"fd").iterdir())
        owned=set()
        for fd in fds:
            with contextlib.suppress(FileNotFoundError):
                link=fd.readlink().as_posix()
                if link.startswith("socket:["):owned.add(link[8:-1])
        active={}
        for proto in ("tcp","udp"):
            count=0
            for family in ("","6"):
                for row in (self.proc/"net"/(proto+family)).read_text().splitlines()[1:]:
                    fields=row.split()
                    if fields[9] in owned and int(fields[2].rsplit(":",1)[1],16)==self.port:count+=1
            active[proto+"_target"]=count
        out={"label":label,"ms":round((time.monotonic()-self.start_time)*1000),
             "go":int(match[1]),"fd":len(fds),**sizes,**active}
        with self.out.open("a") as f: print(json.dumps(out),file=f)
        return out

    def start(self):
        if self.thread is not None:raise RuntimeError("node sampler already running")
        self.stopped.clear()
        self.failure=None
        self.start_time=time.monotonic()
        self.sample("start")
        def run():
            try:
                for minute in range(5,46,5):
                    if self.stopped.wait(max(0,self.start_time+minute*60-time.monotonic())):return
                    self.sample(str(minute))
            except Exception as e:self.failure=e
        self.thread=threading.Thread(target=run,daemon=True)
        self.thread.start()

    def stop(self):
        if self.thread is None:return
        self.stopped.set()
        self.thread.join()
        self.thread=None
        if self.failure:raise self.failure
        last=self.sample("disconnected")
        if last["tcp_target"] or last["udp_target"]:raise RuntimeError("echo connection survived VPN stop")
