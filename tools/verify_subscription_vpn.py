"""LDPlayer acceptance with disposable WSL peers, Android document import and the stock browser."""
import argparse
import base64
import json
import contextlib
import ipaddress
from pathlib import Path
import queue
import re
import secrets
import shlex
import subprocess
import tempfile
import threading
import time
import xml.etree.ElementTree as ET

PROTOCOLS = ("vless", "vmess", "trojan", "ss", "hysteria2", "tuic", "anytls")
PACKAGE = "dev.nexus.localclient"
RUNNER = PACKAGE + ".test/" + PACKAGE + ".CatalogCheck"


def verify(args):
    if not re.fullmatch(r"emulator-\d+", args.serial):
        raise ValueError("an emulator serial is required")
    adb = ["adb", "-s", args.serial]
    def call(*parts, timeout=20, check=True):
        return subprocess.run(adb + list(parts), capture_output=True, text=True, encoding="utf8",
                              errors="replace", timeout=timeout, check=check).stdout
    def tree():
        raw = call("exec-out","uiautomator","dump","/dev/tty")
        return ET.fromstring(raw[raw.index("<?xml"):raw.index("</hierarchy>")+12])
    def tap(label, scroll=False):
        for i in range(8):
            for n in tree().iter("node"):
                if label not in (n.get("text"),n.get("content-desc"),n.get("resource-id")):
                    continue
                x,y,right,bottom = map(int,re.findall(r"\d+",n.get("bounds")))
                if bottom-y < 12:
                    continue
                call("shell","input","tap",str((x+right)//2),str((y+bottom)//2))
                return
            if scroll:
                call("shell","input","swipe","750","790","750","280","350")
            else:
                time.sleep(.3)
        raise AssertionError("UI did not offer "+label)
    def screenshot(label):
        if args.screens:
            args.screens.mkdir(exist_ok=True,parents=True)
            with (args.screens/(label+".png")).open("wb") as dest:
                subprocess.run(adb+["exec-out","screencap","-p"],stdout=dest,check=True,timeout=12)
    def launch(url):
        call("shell","am","start","-W","-a","android.intent.action.VIEW","-d",url,"--es","com.android.browser.application_id",PACKAGE,"-p",args.browser)
    def page(marker,url):
        error = ""
        for _ in range(3):
            nodes = list(tree().iter("node"))
            values = [v for n in nodes if n.get("package")==args.browser
                      for v in (n.get("text",""),n.get("content-desc",""))]
            if any(marker in v for v in values) and any(url.split("://",1)[1] in v for v in values):
                return
            if any("ERR_" in v for v in values):
                error = next(v for v in values if "ERR_" in v)[:80]
                if any(n.get("package") == args.browser and n.get("resource-id") == "android:id/button1" for n in nodes):
                    tap("android:id/button1")
        # Some LDPlayer WebViews draw the page without publishing accessibility text.
        pid = call("shell","pidof",args.browser).strip()
        if not pid.isdecimal():
            raise AssertionError("browser process unavailable")
        port = call("forward","tcp:0","localabstract:webview_devtools_remote_"+pid).strip()
        try:
            assert port.isdecimal()
            subprocess.run(["node","--input-type=module","-e",r"""
                const [port,url,marker] = process.argv.slice(1);
                const targets = await (await fetch("http://127.0.0.1:"+port+"/json/list")).json();
                const page = targets.find(t => t.type === "page" && t.url === url);
                if (!page) process.exit(1);
                const ws = new WebSocket(page.webSocketDebuggerUrl);
                const timer = setTimeout(() => process.exit(1),4000);
                ws.onopen = () => ws.send(JSON.stringify({id:1,method:"Runtime.evaluate",params:{
                    expression:"document.visibilityState==='visible' && document.body.innerText.includes("+JSON.stringify(marker)+")",
                    returnByValue:true}}));
                ws.onmessage = ({data}) => { const response=JSON.parse(data);
                    if (response.id === 1) { clearTimeout(timer);ws.close();process.exit(response.result?.result?.value === true ? 0 : 1); }
                };
            """,port,url,marker],check=True,timeout=8,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        except (OSError,subprocess.SubprocessError):
            raise AssertionError("visible browser page did not display "+marker+" "+error) from None
        finally:
            call("forward","--remove","tcp:"+port)
    def probe(vpn=True):
        mode = "on" if vpn else "off"
        output = call("shell","am","instrument","-w","-e","subscription","on","-e","vpn",mode,"-e","dns","192.0.2.53",
                      "-e","echo","192.0.2.5","-e","port","8053","dev.nexus.vpnprobe/.Probe",timeout=75)
        required = ["example.com=HTTPS 200","example.org=HTTPS 200","INSTRUMENTATION_CODE: -1"]
        required += ["default=VPN","udp=32,1200,1472"] if vpn else ["default=ordinary network"]
        if any(value not in output for value in required):
            raise AssertionError("independent UID: "+output.strip()[:450])

    def outage():
        def local(cmd):
            return subprocess.check_output([args.ld,"-s",str(args.ld_index),cmd],timeout=12).decode(errors="replace").strip()
        assert local("getprop ro.serialno") == call("shell","getprop","ro.serialno").strip()
        original = {key:local("settings get global "+name) for key,name in [("wifi","wifi_on"),("data","mobile_data")]}
        assert original["wifi"] == "1" and original["data"] in ("0","1")
        peer = str(ipaddress.IPv4Address(args.host))
        peer_route = "ip -4 route get "+peer+" oif wlan0 2>&1"
        assert "dev wlan0" in local(peer_route), "missing physical route to the QA peer before outage"
        try:
            local("svc data disable")
            local("svc wifi disable")
            until = time.monotonic()+120
            while time.monotonic()<until:
                state = local("run-as "+PACKAGE+" cat cache/subscription-qa-state")
                # LDPlayer retains the wlan0 address after withdrawing its routes.
                if state in ("waiting","retry"):
                    assert local("settings get global wifi_on") == "0" and local("settings get global mobile_data") == "0"
                    assert "unreachable" in local(peer_route).lower(), "QA peer can still be reached over Wi-Fi"
                    for family in ("-4","-6"):
                        routes = local("ip "+family+" route show table all")
                        defaults = re.findall(r"^default (?:via \S+ )?dev (\S+)",routes,re.MULTILINE)
                        assert any(re.fullmatch(r"tun\d+",dev) for dev in defaults)
                        assert all(dev == "dummy0" or re.fullmatch(r"tun\d+",dev) for dev in defaults)
                    print("Offline: QA peer has no physical route, both VPN defaults retained, state "+state,flush=True)
                    break
                time.sleep(.5)
            else:
                raise AssertionError("VPN did not wait with physical networks offline")
        finally:
            for key,value in original.items():
                local("svc "+key+(" enable" if value=="1" else " disable"))
            # The vendor briefly exposes the old ADB transport while Wi-Fi is restarting.
            deadline = time.monotonic()+60
            stable = 0
            while time.monotonic()<deadline:
                usable = "dev wlan0" in local(peer_route) and call("shell","echo","online",check=False).strip()=="online"
                stable = stable + 1 if usable else 0
                if stable >= 4:
                    break
                time.sleep(1)
            else:
                raise AssertionError("physical route or emulator ADB did not return")

    def state():
        return call("shell","run-as",PACKAGE,"cat","cache/subscription-qa-state",check=False).strip()
    def wait_state(test, accept, timeout):
        deadline = time.monotonic()+timeout
        last = ""
        while time.monotonic()<deadline:
            last = state()
            if accept(last):
                return last
            if test.poll() is not None:
                raise AssertionError("app test exited: "+test.stdout.read()[-320:])
            time.sleep(.5)
        raise AssertionError("VPN state timeout: "+last[:60])
    with tempfile.TemporaryDirectory(dir=args.temp_dir) as scratch, contextlib.ExitStack() as stack:
        token = secrets.token_hex(4)
        name = "Nexus-QA-"+token+".json"
        target = Path(scratch)/name
        linux = subprocess.check_output(["wsl","-d",args.distro,"--","wslpath","-u",target.resolve().as_posix()],text=True).strip()
        fixture = subprocess.Popen(["wsl","-d",args.distro,"--","python3","-u","tools/compat_lab.py","--full-device",
                    "--host",args.host,"--xray",args.xray,"--shoes",args.shoes,"--fixture",linux],
                    stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True,encoding="utf8",errors="replace")
        events = queue.Queue()
        def collect():
            for line in fixture.stdout:
                events.put(line.strip())
            events.put("EOF")
        threading.Thread(target=collect,daemon=True).start()
        def command(cmd):
            fixture.stdin.write(cmd+"\n")
            fixture.stdin.flush()
        def close_fixture():
            if fixture.poll() is None:
                command("quit")
            fixture.wait(20)
        stack.callback(close_fixture)
        def event(prefix, timeout=25):
            until=time.monotonic()+timeout
            while True:
                row=events.get(timeout=max(.01,until-time.monotonic()))
                if row=="EOF":
                    raise RuntimeError("fixture exited")
                if row.startswith(prefix):
                    return row
        event("READY",60)
        print("Disposable test peers ready",flush=True)
        if args.links:
            bundle=json.loads(target.read_text(encoding="utf8"))
            links=[n["config"].split("#")[0]+"#"+n["type"] for n in bundle["nodes"]]
            name=target.stem+".txt"
            target=target.with_suffix(".txt")
            target.write_bytes(base64.b64encode(("\n".join(links)).encode()))
        remote="/sdcard/Download/"+name
        call("push",str(target),remote)
        stack.callback(call,"shell","rm","-f",remote)
        call("shell","am","start","-W","-f","0x04000000","-n",PACKAGE+"/.MainActivity")
        tap("节点")
        tap("添加节点",True)
        tap("从文件或图片导入",True)
        # A fresh file appears first when Downloads is sorted by modification time.
        for _ in range(2):
            if any(n.get("text")==name for n in tree().iter("node")):
                break
            tap("显示根目录")
            tap("下载")
        def remove_import():
            call("shell","am","force-stop",PACKAGE)
            output=call("shell","am","instrument","-w","-e","mode","remove-qa-source","-e","source",name,RUNNER)
            if "INSTRUMENTATION_CODE: -1" not in output:
                raise RuntimeError("test source cleanup failed")
        stack.callback(remove_import)
        tap(name,True)
        for _ in range(5):
            labels = [n.get("text","") for n in tree().iter("node")]
            if name in labels or "已导入 1 / 1 个文件" in labels:
                break
        else:
            raise AssertionError("document import was not confirmed")
        print("Subscription imported via Android document picker",flush=True)
        if args.screens:
            for _ in range(6):
                if any(n.get("text","").lower() in PROTOCOLS for n in tree().iter("node")):
                    break
                call("shell","input","swipe","750","770","750","320","300")
            screenshot("subscription-list")
        def traffic(protocol,stage):
            nonce=secrets.token_hex(6)
            receipt_url="http://192.0.2.5/check/"+nonce
            launch(receipt_url)
            receipt=event("/check/"+nonce+" ",20).split()[-1]
            if not receipt.startswith(protocol+"-"):
                raise AssertionError("unexpected egress: "+receipt)
            print(f"{stage}: node issued {receipt}",flush=True)
            page(receipt,receipt_url)
            screenshot(stage.replace(" ","-")+"-"+protocol)
            public_url="https://example.com/?nexus="+nonce
            launch(public_url)
            page("Example Domain",public_url)
            try:
                probe()
            except AssertionError as failure:
                if "TimeoutException" not in str(failure):
                    raise
                print(f"{protocol} {stage}: independent request timed out; checking a fresh request",flush=True)
                probe()
            print(f"{protocol} {stage}: browser receipt {receipt}, public HTTPS, independent DNS/TCP/UDP",flush=True)
        failed=[]
        for typ in args.protocol or PROTOCOLS:
            call("shell","am","force-stop",PACKAGE)
            print("Testing "+typ,flush=True)
            inst=["am","instrument","-w","-e","mode","subscription-vpn","-e","source",name,"-e","protocol",typ,RUNNER]
            driver=[args.ld,"-s",str(args.ld_index),shlex.join(inst)] if args.ld else adb+["shell"]+inst
            test=subprocess.Popen(driver,stdout=subprocess.PIPE,text=True,encoding="utf8",errors="replace")
            down=False
            try:
                active=wait_state(test,lambda s:s in PROTOCOLS,75)
                if active!=typ:
                    raise AssertionError(f"initial node was {active}")
                traffic(typ,"connected")
                command("down "+typ)
                down=True
                event("down "+typ)
                alternate=wait_state(test,lambda s:s in PROTOCOLS and s!=typ,180)
                traffic(alternate,"after loss of "+typ)
                if args.ld and typ == (args.protocol or PROTOCOLS)[-1]:
                    outage()
                    recovered = wait_state(test,lambda s:s in PROTOCOLS,90)
                    traffic(recovered,"network recovery")
                    call("shell","am","start","-W","-n",PACKAGE+"/.MainActivity")
                    tap("节点")
                    tap("测量当前连接速度",True)
                    for _ in range(18):
                        values=[n.get("text","") for n in tree().iter("node")]
                        measured=next((v for v in values if "Mbps" in v and "上传" in v),"")
                        if measured:
                            print(measured,flush=True)
                            break
                    else:
                        raise AssertionError("speed measurement absent")

            except Exception as exc:
                failed.append(typ)
                print("FAIL",typ,type(exc).__name__,str(exc)[:400],flush=True)
            finally:
                if down:
                    command("up "+typ)
                    event("up "+typ)
                if test.poll() is None:
                    call("shell","run-as",PACKAGE,"touch","cache/subscription-qa-stop")
                result=test.communicate(timeout=30)[0]
                if "INSTRUMENTATION_CODE: -1" not in result:
                    failed.append(typ)
        probe(False)
        if re.search(r"\d+: tun\d+[:@]",call("shell","ip","-o","link","show")):
            raise AssertionError("TUN remained after disconnect")
        print("Disconnected: ordinary network and both public HTTPS pages verified",flush=True)
        if failed:
            raise RuntimeError("incomplete protocols: "+", ".join(sorted(set(failed))))


if __name__=="__main__":
    parser=argparse.ArgumentParser()
    for arg in ("serial","distro","host","xray","shoes","temp-dir"):
        parser.add_argument("--"+arg,required=True)
    parser.add_argument("--browser",default="com.android.browser")
    parser.add_argument("--ld",help="LDPlayer ld.exe; also test a physical-network outage and an on-demand speed measurement")
    parser.add_argument("--ld-index",type=int,default=0)
    parser.add_argument("--links",action="store_true",help="import a standard Base64 share-link subscription")
    parser.add_argument("--screens",type=Path)
    parser.add_argument("--protocol",action="append",choices=PROTOCOLS)
    verify(parser.parse_args())
