"""Install a new Nexus node on supported Debian and Ubuntu releases. Run from the private SSH upload directory."""
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import platform
import pwd
import shutil
import subprocess
import sys
import tempfile
import time

BASE = Path("/opt/nexus-node")
STATE = Path("/var/lib/nexus-node")
UNITS = Path("/etc/systemd/system")
USER = "nexus-node"
SERVICES = ("nexus-node.service", "nexus-claim.service")

def run(*command, data=None):
    return subprocess.run(command, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          check=True, timeout=45).stdout

def install(cfg):
    release = platform.freedesktop_os_release()
    supported = {("debian", "12"), ("debian", "13"), ("ubuntu", "24.04")}
    if os.geteuid() != 0 or (release.get("ID"), release.get("VERSION_ID")) not in supported:
        raise ValueError("Debian 12/13 or Ubuntu 24.04 with root privileges required")
    if not Path("/run/systemd/system").is_dir() or not shutil.which("openssl"):
        raise ValueError("systemd and OpenSSL are required")
    # A directory lock leaves no file behind and serializes simultaneous installations.
    lock = os.open("/opt", os.O_RDONLY | os.O_DIRECTORY)
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if cfg.get("action") == "migration-export":
            switch_installation(False)
            try:
                script = Path(__file__).with_name("node_admin.py").read_text()
                data = run("runuser","-u",USER,"--","python3","-I","-c",script,str(STATE/"identity"),"migration-export")
                return json.loads(data)
            except BaseException:
                switch_installation(True)
                raise
        if cfg.get("action") in ("archive","reactivate"):
            return switch_installation(cfg["action"]=="reactivate")
        if cfg.get("action") in ("upgrade","rollback"):
            return maintain(cfg)
        return install_locked(cfg)
    finally:
        os.close(lock)

def switch_installation(restore):
    paths=[(BASE,BASE.with_name("nexus-node.removed"))]
    paths.extend((UNITS/name,UNITS/(name+".nexus-removed")) for name in SERVICES)
    moves=[(b,a) for a,b in paths] if restore else paths
    for old,new in moves:
        if old.is_symlink() or old.stat().st_uid!=0 or new.exists() or new.is_symlink():
            raise ValueError("installation path changed")
    for (unit,_),name in zip(moves[1:],SERVICES):
        expected = str(BASE/"nexus-server") if name==SERVICES[0] else str(BASE/"node_admin.py")
        if expected not in unit.read_text() or "User=nexus-node\n" not in unit.read_text():
            raise ValueError("unexpected unit")
        if restore and run("systemctl","show","-p","LoadState","--value",name).strip()!=b"not-found":
            raise ValueError("another unit already owns this name")
    moved=[]
    try:
        if not restore:
            run("systemctl","disable","--now",*SERVICES)
        for old,new in moves:
            os.rename(old,new)
            moved.append((old,new))
        run("systemctl","daemon-reload")
        if restore:
            run("systemctl","enable","--now",*SERVICES)
            for unit in SERVICES: run("systemctl","is-active","--quiet",unit)
    except BaseException:
        if restore and len(moved)==len(moves):
            subprocess.run(["systemctl","disable","--now",*SERVICES],capture_output=True)
        for old,new in reversed(moved): os.rename(new,old)
        run("systemctl","daemon-reload")
        if not restore: run("systemctl","enable","--now",*SERVICES)
        raise
    return {"active":restore,"removed":not restore}

def maintain(cfg):
    files = ("nexus-server","node_admin.py")
    previous = BASE/"previous"
    if platform.machine()!=cfg["arch"] or BASE.is_symlink() or BASE.stat().st_uid!=0:
        raise ValueError("unexpected installation")
    for name in SERVICES:
        unit = UNITS/name
        if unit.is_symlink() or unit.stat().st_uid!=0 or str(BASE)+"/" not in unit.read_text():
            raise ValueError("unit is not a Nexus installation")
    for name in files:
        if (BASE/name).is_symlink() or (BASE/name).stat().st_uid!=0:
            raise ValueError("unexpected installed file")
    with tempfile.TemporaryDirectory(dir=BASE) as temp:
        work = Path(temp)
        old = work/"old"
        old.mkdir()
        for name in files:
            shutil.copy2(BASE/name,old/name)
            src = (previous if cfg["action"]=="rollback" else Path(__file__).resolve().parent)/name
            shutil.copyfile(src,work/name)
            if cfg["action"]=="upgrade" and hashlib.sha256((work/name).read_bytes()).hexdigest()!=cfg["sha256"][name]:
                raise ValueError("uploaded file digest mismatch")
            (work/name).chmod(0o755 if name=="nexus-server" else 0o644)
        def restart():
            run("systemctl","restart",*SERVICES)
            time.sleep(2)
            for unit in SERVICES:
                run("systemctl","is-active","--quiet",unit)
        try:
            for name in files:
                os.replace(work/name,BASE/name)
            restart()
            if previous.exists():
                os.rename(previous,work/"retired")
            os.rename(old,previous)
        except BaseException:
            for name in files:
                os.replace(old/name,BASE/name)
            if (work/"retired").exists():
                os.rename(work/"retired",previous)
            restart()
            raise
    return {"action":cfg["action"],"active":True}

def install_locked(cfg):
    if BASE.exists() or STATE.exists() or any((UNITS/n).exists() or run("systemctl","show","-p","LoadState","--value",n).strip()!=b"not-found" for n in SERVICES):
        raise ValueError("existing installation or unit; no changes made")
    try:
        pwd.getpwnam(USER)
    except KeyError:
        pass
    else:
        raise ValueError("account name already in use")
    if platform.machine()!=cfg["arch"]:
        raise ValueError("wrong server architecture")
    host = ipaddress.ip_address(cfg["host"])
    resolver = ipaddress.IPv4Address(cfg["dns"])
    port = int(cfg["port"])
    claim_port = int(cfg["claim_port"])
    if min(port,claim_port)<1024 or max(port,claim_port)>65535 or port==claim_port:
        raise ValueError("invalid ports")
    upload = Path(__file__).resolve().parent
    for name in ("nexus-server","node_admin.py"):
        if hashlib.sha256((upload/name).read_bytes()).hexdigest()!=cfg["sha256"][name]:
            raise ValueError("uploaded file digest mismatch")
    made_user = made_base = made_state = False
    written = []
    try:
        run("useradd","--system","--user-group","--no-create-home","--home-dir",str(STATE),"--shell","/usr/sbin/nologin",USER)
        made_user = True
        BASE.mkdir(mode=0o755)
        made_base = True
        for name in ("nexus-server","node_admin.py"):
            shutil.copyfile(upload/name,BASE/name)
            (BASE/name).chmod(0o755 if name=="nexus-server" else 0o644)
        STATE.mkdir(mode=0o700)
        made_state = True
        shutil.chown(STATE,USER,USER)
        admin = ["runuser","-u",USER,"--","python3",str(BASE/"node_admin.py"),str(STATE/"identity")]
        run(*admin,"init",data=json.dumps(cfg).encode())
        profile = json.loads(run(*admin,"owner",data=json.dumps({"name":cfg["name"],"pub":cfg["pub"]}).encode()))
        identity = STATE/"identity"
        listen = "[::]" if host.version==6 else "0.0.0.0"
        commands = (f"{BASE}/nexus-server {listen}:{port} ca.der server.der server.key.der registry:devices.db --internet {resolver}:53",
                    f"/usr/bin/python3 {BASE}/node_admin.py {identity} serve")
        for name,command in zip(SERVICES,commands):
            hide = f"InaccessiblePaths={identity}/ca.key\n" if name==SERVICES[0] else ""
            unit = (f"[Unit]\nDescription=Nexus private node\nAfter=network.target\n[Service]\nUser={USER}\nGroup={USER}\n"
                    f"WorkingDirectory={identity}\nExecStart={command}\nRestart=on-failure\nRestartSec=3\nUMask=0077\n"
                    f"NoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\nProtectHome=true\nReadWritePaths={STATE}\n"
                    f"{hide}[Install]\nWantedBy=multi-user.target\n")
            with (UNITS/name).open("x") as dest:
                written.append(name)
                dest.write(unit)
        run("systemctl","daemon-reload")
        run("systemctl","enable","--now",*SERVICES)
        time.sleep(1)
        for name in SERVICES:
            run("systemctl","is-active","--quiet",name)
        return profile
    except BaseException:
        for name in written:
            subprocess.run(["systemctl","disable","--now",name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            (UNITS/name).unlink(missing_ok=True)
        if written:
            run("systemctl","daemon-reload")
        if made_state:
            shutil.rmtree(STATE)
        if made_base:
            shutil.rmtree(BASE)
        if made_user:
            run("userdel",USER)
        raise

if __name__=="__main__":
    try:
        print(json.dumps(install(json.load(sys.stdin))))
    except Exception as exc:
        # Do not echo input or subprocess output, which may contain enrollment material.
        print(type(exc).__name__,file=sys.stderr)
        sys.exit(1)
