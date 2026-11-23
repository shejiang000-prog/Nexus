"""Run M3 instrumentation against the live, disposable android_v2_lab.py fixture."""
import argparse
import contextlib
import json
from pathlib import Path
import subprocess
import tempfile
import time


@contextlib.contextmanager
def advanced_clock(shell):
    """Shift only the disposable guest clock, preserving elapsed test time on restore."""
    base = int(shell("date","+%s",check=True).stdout)
    started = time.monotonic()
    automatic = shell("cmd","time_detector","is_auto_detection_enabled",check=True).stdout.strip().decode()
    if automatic not in ("true","false"):
        raise RuntimeError("unknown guest clock settings")
    def set_time(seconds):
        uptime = float(shell("cat","/proc/uptime",check=True).stdout.split()[0])
        shell("cmd","time_detector","suggest_manual_time","--elapsed_realtime",str(int(uptime*1000)),
              "--unix_epoch_time",str(int(seconds*1000)),check=True)
        if abs(int(shell("date","+%s",check=True).stdout) - seconds) > 4:
            raise RuntimeError("guest clock did not reach requested time")
    try:
        shell("cmd","time_detector","set_auto_detection_enabled","false",check=True)
        set_time(base + 12*3600)
        yield
    finally:
        try: set_time(base + time.monotonic() - started)
        finally: shell("cmd","time_detector","set_auto_detection_enabled",automatic,check=True)


def run(adb, package, fixture, runner, probe, phase):
    a = [str(adb), "-s", "emulator-5554"]
    def shell(*args, **kw):
        return subprocess.run([*a, "shell", *args], stdout=subprocess.PIPE,
                              stderr=subprocess.DEVNULL, timeout=15, **kw)
    reports = shell("pm","list","instrumentation",package,check=True).stdout.decode().splitlines()
    runners = [line.split()[0].removeprefix("instrumentation:") for line in reports
               if line.endswith("(target="+package+")") and "/dev.nexus.localclient.CatalogCheck " in line]
    if runner is None and len(runners)==1:
        runner = runners[0]
    if runner not in runners:
        raise RuntimeError("missing or ambiguous CatalogCheck for selected target; pass --runner")
    shell("run-as",package,"mkdir","-p","no_backup",check=True)
    cfg = json.loads(fixture.read_bytes())
    # Neither the invitation nor the control capability appears in argv, instrumentation output, or shared storage.
    data = fixture.read_bytes()
    private_file = "no_backup/m3.json"
    shell("run-as",package,"touch",private_file,check=True)
    shell("run-as",package,"chmod","600",private_file,check=True)
    subprocess.run([*a,"shell","-T","run-as",package,"tee",private_file],
                   input=data, stdout=subprocess.DEVNULL, check=True, timeout=12)
    if int(shell("run-as",package,"stat","-c","%s",private_file,check=True).stdout) != len(data):
        raise RuntimeError("incomplete private instrumentation configuration")
    shell("run-as",package,"rm","-f","no_backup/m3-stage","no_backup/m3-probe-ok",check=True)
    mode = "m32-expiry" if phase == "expiry" else "m3"
    with (advanced_clock(shell) if phase == "expiry" else contextlib.nullcontext()), tempfile.TemporaryFile() as log:
        proc=subprocess.Popen([*a,"shell","am","instrument","-w","-e","mode",mode,"-e","phase",phase,"-e","probePackage",probe.split("/")[0],runner], stdout=log,stderr=subprocess.STDOUT)
        stage=""
        deadline=time.monotonic()+720
        try:
            while proc.poll() is None:
                if time.monotonic()>deadline:raise TimeoutError("M3 instrumentation watchdog")
                if phase in ("blackhole","rate","loss","healthy-download","idle-h2","idle-h3"):
                    # Let Android sleep without host polling the device. Only all/regression require coordination.
                    time.sleep(2)
                    continue
                marker=shell("run-as",package,"cat","no_backup/m3-stage").stdout.decode().strip()
                if marker !=stage and marker != "" and len(marker)<64:
                    stage=marker
                    print("stage",stage,flush=True)
                    if stage in ("probe-v2","probe-scoped","probe-xray"):
                        output=subprocess.check_output([*a,"shell","am","instrument","-w","-e","m3","yes","-e","vpn","on",
                            "-e","echo",cfg["target"],"-e","port",str(cfg["echoPort"]),probe],timeout=40)
                        if b"INSTRUMENTATION_CODE: -1" not in output:raise RuntimeError("separate UID probe failed: "+output.decode(errors="replace"))
                        print(stage,"separate UID TCP and UDP passed",flush=True)
                        shell("run-as",package,"touch","no_backup/m3-probe-ok",check=True)
                time.sleep(.3)
            log.seek(0);out=log.read().decode(errors="replace");print(out,flush=True)
            if proc.returncode or "INSTRUMENTATION_CODE: -1" not in out:raise RuntimeError("M3 instrumentation failed")
        finally:
            if proc.poll() is None:
                proc.terminate();proc.wait(timeout=10)
                shell("am","force-stop",package)
            shell("run-as",package,"rm","-f","no_backup/m3.json","no_backup/m3-stage","no_backup/m3-probe-ok")


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("adb",type=Path);p.add_argument("package",choices=["dev.nexus.lab.m0","dev.nexus.localclient"])
    p.add_argument("fixture",type=Path)
    p.add_argument("--runner",help="installed instrumentation component; auto-detected for the target package")
    p.add_argument("--probe",default="dev.nexus.vpnprobe/.Probe",choices=["dev.nexus.vpnprobe/.Probe","dev.nexus.lab.m31.probe/dev.nexus.vpnprobe.Probe"])
    p.add_argument("--phase",choices=["all","switch","recovery","regression","expiry","blackhole","rate","loss","healthy-download","idle-h2","idle-h3"],default="all")
    opt=p.parse_args()
    run(opt.adb,opt.package,opt.fixture,opt.runner,opt.probe,opt.phase)
