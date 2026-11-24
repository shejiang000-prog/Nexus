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
PHASES = {"invite","links","bench","block","v2","manage","restart"}


class Device:
    def __init__(self, adb, serial, package):
        if not re.fullmatch(r"[a-zA-Z0-9_.]+",package):
            raise ValueError("invalid package")
        self.adb = [str(adb),"-s",serial]
        self.package = package
        self.prefix = ["run-as",package]
        listing = self.shell("pm","list","instrumentation",package).decode().splitlines()
        self.runner, = [line.split()[0][len("instrumentation:"):] for line in listing
                        if ".CatalogCheck " in line and line.endswith("(target="+package+")")]
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
        if value not in {"arm-drop","blocked","arm-unblock","arm-restart","node-up"}:
            raise ValueError("unknown control signal")
        self.write("no_backup/m4-signal",value)

    def start(self,phase,**opts):
        if phase not in PHASES:
            raise ValueError("invalid phase")
        self.shell(*self.prefix,"rm","-f","no_backup/m4-stage","no_backup/m4-signal")
        args=["shell","am","instrument","-w","-e","mode","m4-probe","-e","phase",phase]
        for key,value in opts.items():
            if key not in {"pairs","protocol"} or not re.fullmatch(r"[a-z0-9]+",str(value)):
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
            out[key]=value.strip()
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
            reboot()
            wait_new_boot()
            self.signal("node-up")
            return self.finish(proc,430)
        finally:
            if proc.poll() is None:
                proc.terminate()
                with suppress(Exception): proc.wait(timeout=10)
                self.shell("am","force-stop",self.package)

    def block(self,drop,restore):
        """Pass existing host-side callables. restore must be idempotent and remove only our own rule."""
        proc=self.start("block")
        armed=False
        try:
            self.wait(proc,"ready-drop")
            drop_arm_min=time.monotonic()
            self.signal("arm-drop")
            self.wait(proc,"armed-drop")
            drop_start=time.monotonic()
            armed=True
            drop()
            drop_end=time.monotonic()
            self.signal("blocked")
            self.wait(proc,"ready-unblock")
            remove_arm_min=time.monotonic()
            self.signal("arm-unblock")
            self.wait(proc,"armed-unblock")
            restore_start=time.monotonic()
            restore()
            restore_end=time.monotonic()
            armed=False
            out=self.finish(proc,200)
            out["host-rule-install-ms"]=round(1000*(drop_end-drop_start))
            out["host-rule-remove-ms"]=round(1000*(restore_end-restore_start))
            # The device timestamps arm when acknowledging it. SSH applies its rule between
            # callback start and completion. Keep both uncertainties in the reported range.
            for name,metric,arm_min,apply_end in (
                ("hot","hot-H2-fresh-https-ms-from-arm",drop_arm_min,drop_end),
                ("recover","restored-H3-fresh-https-ms-from-arm",remove_arm_min,restore_end),
            ):
                ms=int(out[metric])
                out[name+"-from-rule-ms-bounds"]=[max(0,ms-round(1000*(apply_end-arm_min))),ms]
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
    sub=p.add_subparsers(dest="action",required=True)
    c=sub.add_parser("config"); c.add_argument("--host"); c.add_argument("--probe-url",default=FIXED_FILE["probeUrl"])
    q=sub.add_parser("run"); q.add_argument("phase",choices=sorted(PHASES)); q.add_argument("--pairs",type=int)
    q.add_argument("--protocol",choices=["reality","hysteria2"])
    sub.add_parser("stage")
    s=sub.add_parser("signal"); s.add_argument("value",choices=["arm-drop","blocked","arm-unblock","arm-restart","node-up"])
    a=p.parse_args()
    device=Device(a.adb,a.serial,a.package)
    if a.action=="config": device.config(a.host,probeUrl=a.probe_url)
    elif a.action=="run":
        print(json.dumps(device.run(a.phase,**{k:v for k,v in vars(a).items() if k in ("pairs","protocol") and v is not None}),ensure_ascii=False))
    elif a.action=="stage": print(device.stage())
    elif a.action=="signal": device.signal(a.value)
