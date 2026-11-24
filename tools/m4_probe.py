"""Host-side single-device M4 controls. Private exports stay in app-private storage."""
import argparse
from contextlib import suppress
import json
import re
import subprocess
import time

FIXED_FILE = {
    "url": "https://www.python.org/ftp/python/3.12.8/python-3.12.8-embed-amd64.zip",
    "bytes": 11094114,
    "sha256": "8d3f33be9eb810f23c102f08475af2854e50484b8e4e06275e937be61ce3d2fb",
    "probeUrl": "https://example.com/",
}
PHASES = {"reference","enroll","invite","links","bench","block","v2","manage","restart"}


class Device:
    def __init__(self, adb, serial, package, runner=None):
        if not re.fullmatch(r"[a-zA-Z0-9_.]+",package):
            raise ValueError("invalid package")
        self.adb = [str(adb),"-s",serial]
        self.package = package
        self.prefix = ["run-as",package]
        listing = self.shell("pm","list","instrumentation",package).decode().splitlines()
        runners = [line.split()[0][len("instrumentation:"):] for line in listing
                   if ".CatalogCheck " in line and line.endswith("(target="+package+")")]
        if runner is None and len(runners) == 1:
            runner = runners[0]
        if runner not in runners:
            raise RuntimeError("missing or ambiguous M4 test runner; select the installed runner")
        self.runner = runner
        self.shell(*self.prefix,"mkdir","-p","no_backup")

    def shell(self,*args):
        r = subprocess.run(self.adb+["shell",*args],capture_output=True,timeout=15)
        if r.returncode:
            raise RuntimeError("device command failed")
        return r.stdout

    def write(self,path,data):
        # Use stdin, suppress tee stdout, avoid shared storage and host materialization.
        if not isinstance(data,bytes):
            data=data.encode()
        temp=path+".part"
        self.shell(*self.prefix,"touch",temp)
        self.shell(*self.prefix,"chmod","600",temp)
        r=subprocess.run(self.adb+["shell","-T",*self.prefix,"tee",temp],
                         input=data,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=20)
        if r.returncode or int(self.shell(*self.prefix,"stat","-c","%s",temp))!=len(data):
            raise RuntimeError("incomplete private write")
        self.shell(*self.prefix,"mv",temp,path)

    def config(self,host=None,**overrides):
        cfg=dict(FIXED_FILE,**overrides)
        if host: cfg["host"]=host
        self.write("no_backup/m4.json",json.dumps(cfg))

    def stage(self):
        r=subprocess.run(self.adb+["shell",*self.prefix,"cat","no_backup/m4-stage"],capture_output=True,timeout=15)
        return r.stdout.decode().strip() if not r.returncode else ""

    def signal(self,value):
        if not re.fullmatch(r"(arm-drop|arm-unblock|arm-restart|node-up|(blocked|restored) [0-9]+)",value):
            raise ValueError("unknown control signal")
        self.write("no_backup/m4-signal",value)

    def start(self,phase,**opts):
        if phase not in PHASES:
            raise ValueError("invalid phase")
        self.shell(*self.prefix,"rm","-f","no_backup/m4-stage","no_backup/m4-signal")
        args=["shell","am","instrument","-w","-e","mode","m4-probe","-e","phase",phase]
        for key,value in opts.items():
            if key not in {"pairs","protocol","recovery"} or not re.fullmatch(r"[a-z0-9]+",str(value)):
                raise ValueError("invalid options")
            args+=["-e",key,str(value)]
        return subprocess.Popen(self.adb+args+[self.runner],stdout=subprocess.PIPE,stderr=subprocess.STDOUT)

    @staticmethod
    def finish(proc,timeout=1000):
        data = proc.communicate(timeout=timeout)[0].decode(errors="replace")
        # CatalogCheck returns only exception class and stack locations. Do not echo unknown adb logs.
        out={}
        for key,value in re.findall(r"^INSTRUMENTATION_RESULT: ([\w-]+)=(.*)$",data,re.M):
            if key in {"shortMsg","longMsg","stream"}: continue
            out[key]=value.rstrip("\r") if key.startswith("trace-") else value.strip()
        if proc.returncode or "INSTRUMENTATION_CODE: -1" not in data:
            raise RuntimeError("M4 instrumentation failed "+json.dumps(out,ensure_ascii=False))
        return out

    def run(self,phase,**opts):
        return self.finish(self.start(phase,**opts))

    def wait(self,proc,stage,timeout=180):
        end=time.monotonic()+timeout
        while time.monotonic()<end:
            if proc.poll() is not None:
                self.finish(proc)
                raise RuntimeError("test ended before "+stage)
            if self.stage()==stage: return
            time.sleep(.08)
        raise TimeoutError("stage "+stage)

    def restart(self,reboot,wait_new_boot):
        """Reboot the authorized node, then verify a new server boot ID using the caller's SSH session."""
        proc=self.start("restart")
        try:
            self.wait(proc,"ready-restart")
            self.signal("arm-restart")
            self.wait(proc,"armed-restart")
            # A reboot can close SSH before an exit status reaches its caller.
            try:
                reboot()
            except (ConnectionError, TimeoutError, EOFError):
                pass
            wait_new_boot()
            self.signal("node-up")
            return self.finish(proc,430)
        finally:
            if proc.poll() is None:
                proc.terminate()
                with suppress(Exception): proc.wait(timeout=10)
                self.shell("am","force-stop",self.package)

    def rule_marker(self,callback,signal):
        """Map callback return to Android CLOCK_BOOTTIME, then wake instrumentation over a pre-opened pipe."""
        # /proc/uptime is CLOCK_BOOTTIME rounded down to centiseconds. Intersect five
        # host monotonic sample brackets; report the uncertainty instead of treating adb as instantaneous.
        bounds=[]
        for _ in range(5):
            begin=time.monotonic_ns()/1e6
            sample=float(self.shell("cat","/proc/uptime").split()[0])*1000
            end=time.monotonic_ns()/1e6
            bounds.append((sample-end,sample+10-begin))
        low=max(x[0] for x in bounds)
        high=min(x[1] for x in bounds)
        if low>high:
            raise RuntimeError("unstable host to device monotonic clock mapping")
        command=(f"run-as {self.package} sh -c 'read -r record && "
                 'echo "$record" > no_backup/m4-signal.part && '
                 "mv no_backup/m4-signal.part no_backup/m4-signal'")
        with subprocess.Popen(self.adb+["shell","-T",command],stdin=subprocess.PIPE,
                              stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL) as pipe:
            begin=time.monotonic_ns()/1e6
            callback()
            end=time.monotonic_ns()/1e6
            origin=round(end+(low+high)/2)
            pipe.communicate(f"{signal} {origin}\n".encode(),timeout=10)
            if pipe.returncode: raise RuntimeError("could not send rule event")
        return {"returnBootMs":origin,"clockUncertaintyMs":round((high-low)/2+.5,2),
                "commandDurationMs":round(end-begin,2)}

    def block(self,drop,restore,recovery=True):
        """One measured hot/cold round. Caller callbacks manage only this run's deny rule."""
        proc=self.start("block",recovery=str(recovery).lower())
        armed=False
        try:
            self.wait(proc,"ready-drop")
            self.signal("arm-drop")
            self.wait(proc,"armed-drop")
            armed=True
            applied=self.rule_marker(drop,"blocked")
            self.wait(proc,"ready-unblock",160)
            self.signal("arm-unblock")
            self.wait(proc,"armed-unblock")
            removed=self.rule_marker(restore,"restored")
            armed=False
            out=self.finish(proc,335)
            out.update({"deny":applied,"restore":removed})
            for name in ("hot","cold","recover"):
                if name in out: out[name]=json.loads(out[name])
            return out
        finally:
            try:
                if armed: restore()
            finally:
                if proc.poll() is None:
                    proc.terminate()
                    with suppress(Exception): proc.wait(timeout=10)
                    self.shell("am","force-stop",self.package)


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("serial"); p.add_argument("package"); p.add_argument("--adb",default="adb")
    p.add_argument("--runner",help="installed CatalogCheck component for this package")
    sub=p.add_subparsers(dest="action",required=True)
    c=sub.add_parser("config"); c.add_argument("--host","--owner-host",dest="host"); c.add_argument("--v2-host"); c.add_argument("--echo-host"); c.add_argument("--echo-port",type=int); c.add_argument("--probe-title",default="Example Domain"); c.add_argument("--probe-text"); c.add_argument("--probe-url",default=FIXED_FILE["probeUrl"])
    q=sub.add_parser("run"); q.add_argument("phase",choices=sorted(PHASES)); q.add_argument("--pairs",type=int)
    q.add_argument("--protocol",choices=["reality","hysteria2","h2"])
    q.add_argument("--recovery",choices=["true","false"])
    sub.add_parser("stage")
    s=sub.add_parser("signal"); s.add_argument("value",help="arm-drop, arm-unblock, arm-restart, node-up, or stamped blocked/restored event")
    a=p.parse_args()
    device=Device(a.adb,a.serial,a.package,a.runner)
    if a.action=="config": device.config(a.host,probeUrl=a.probe_url,probeTitle=a.probe_title,probeText=a.probe_text,echoHost=a.echo_host,echoPort=a.echo_port,v2Host=a.v2_host or a.host or "")
    elif a.action=="run":
        print(json.dumps(device.run(a.phase,**{k:v for k,v in vars(a).items() if k in ("pairs","protocol","recovery") and v is not None}),ensure_ascii=False))
    elif a.action=="stage": print(device.stage())
    elif a.action=="signal": device.signal(a.value)
