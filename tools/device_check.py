"""Run the release acceptance checks on one explicitly selected Android device."""
import argparse
import re
import subprocess
import sys
from datetime import datetime
from pathlib import Path

PACKAGE = "dev.nexus.android"
RUNNER = PACKAGE + ".test/dev.nexus.localclient.CatalogCheck"
SCREENS = ("first", "connect", "nodes", "server", "settings", "invite", "receive", "connecting")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--apk", required=True, type=Path)
    parser.add_argument("--test-apk", required=True, type=Path)
    parser.add_argument("--output", type=Path, default=Path.home() / "nexus-device-reports")
    parser.add_argument("--font-scale", type=float, choices=[1.3])
    parser.add_argument("--subscription", action="store_true", help="test the first installed subscription with a supported Xray node (requires VPN consent)")
    parser.add_argument("--adb", default="adb")
    args = parser.parse_args()
    directory = args.output / (re.sub(r"[^A-Za-z0-9_.-]", "_", args.serial) + "-" + datetime.now().strftime("%Y%m%d-%H%M%S-%f"))
    directory.mkdir(parents=True)
    report = ["# Android 设备验收", ""]
    failures = []

    def adb(*cmd, timeout=60):
        p = subprocess.run([args.adb, "-s", args.serial, *map(str, cmd)], capture_output=True, timeout=timeout)
        if p.returncode:
            detail = (p.stdout + p.stderr).decode("utf8", "replace")
            reason = re.search(r"INSTALL_FAILED_[A-Z_]+|unauthorized|offline|not found|Permission denied", detail)
            raise RuntimeError(reason[0] if reason else "adb 执行失败")
        return p.stdout.decode("utf8", "replace").strip()

    def shell(*cmd):
        return adb("shell", *cmd)

    def test(mode, *extra):
        params = ["-e", "mode", mode] if mode else []
        output = adb("shell", "am", "instrument", "-w", *params, *extra, RUNNER, timeout=600)
        if not re.search(r"^INSTRUMENTATION_CODE: -1\s*$", output, re.M):
            reason = re.search(r"error=([A-Za-z]+(?:Exception|Error))|shortMsg=(Process crashed)", output)
            raise RuntimeError(next(filter(None,reason.groups())) if reason else "检查未成功结束；请检查设备解锁状态与 VPN 授权")

        if mode == "split-public":
            results = dict(re.findall(r"^INSTRUMENTATION_RESULT: ([\w-]+)=([^\r\n]+)", output, re.M))
            if results.get("physical-CN-vs-global") != "不一致":
                raise RuntimeError("无法区分直连和代理出口，请核对网络及节点")
            expected = {"global-CN-vs-CF": "一致", "bypass-CN-vs-physical": "一致",
                        "bypass-CF-vs-global": "一致", "bypass-CN-vs-global": "不一致"}
            if any(results.get(key) != value for key, value in expected.items()):
                raise RuntimeError("出口对照缺失或与分流模式不符")

    def check(label, operation):
        try:
            operation()
            report.append("- " + label + "　通过")
        except (OSError, RuntimeError, subprocess.TimeoutExpired) as exc:
            failures.append(label)
            report.append("- " + label + "　失败：" + str(exc))
    def set_font(value):
        if value == "null":
            shell("settings", "delete", "system", "font_scale")
        else:
            shell("settings", "put", "system", "font_scale", value)
        if shell("settings", "get", "system", "font_scale") != value:
            raise RuntimeError("系统未应用指定的字体缩放")

    def set_night(value):
        shell("cmd", "uimode", "night", value)
        if shell("cmd", "uimode", "night").split(":")[-1].strip() != value:
            raise RuntimeError("系统未应用指定的夜间模式")

    night = scale = None
    changed_font = changed_night = False
    try:
        if not all(p.is_file() for p in (args.apk,args.test_apk)):
            raise RuntimeError("找不到指定的 APK")
        for label,prop in [("Android","ro.build.version.release"),("API","ro.build.version.sdk"),("厂商","ro.product.manufacturer"),("型号","ro.product.model"),("ROM","ro.build.display.id"),("ABI","ro.product.cpu.abilist")]:
            report.append(f"- {label}　{shell('getprop',prop)}")
        report.append("- 显示尺寸　" + shell("wm","size").replace("\n"," / "))
        scale = shell("settings","get","system","font_scale")
        report.append("- 原始字体缩放　" + scale)
        night = shell("cmd","uimode","night").split(":")[-1].strip()
        if night not in ("yes","no","auto"):
            raise RuntimeError("系统夜间模式无法安全还原")
        policy = shell("dumpsys","window","policy")
        if re.search(r"^\s*(?:showing|mIsShowing)=true\b", policy, re.M):
            raise RuntimeError("请先解锁设备并保持屏幕亮起")
        for apk in (args.apk,args.test_apk):
            if "Success" not in adb("install","-r",apk,timeout=240):
                raise RuntimeError("覆盖安装失败，请核对设备 ABI 和签名")
        for mode in ("simple","simple-overview","simple-motion","release-update",""):
            check(mode or "CatalogCheck", lambda: test(mode))
        if args.subscription:
            check("split-public Xray",lambda:test("split-public","-e","engine","xray","-e","any-source","yes"))
        for size in ["default"] + (["1.3"] if args.font_scale else []):
            if size!="default":
                changed_font = True
                set_font(size)
            for theme,value in [("light","no"),("dark","yes")]:
                changed_night = True
                def shots():
                    set_night(value)
                    test("simple-shots","-e","export","yes")
                    dest=directory/(size+"-"+theme)
                    dest.mkdir()
                    for name in SCREENS:
                        adb("pull",f"/sdcard/Android/data/{PACKAGE}/files/qa-shots/{name}.png",dest/(name+".png"))
                check("simple-shots "+size+" "+theme,shots)
    except (OSError, RuntimeError, subprocess.TimeoutExpired) as exc:
        failures.append("准备");report.append("- 设备准备失败：" + str(exc))
    finally:
        if changed_font:
            check("恢复字体", lambda: set_font(scale))
        if changed_night:
            check("恢复夜间模式", lambda: set_night(night))
        (directory/"report.md").write_text("\n".join(report)+"\n",encoding="utf8")
        print(directory/"report.md")
    return bool(failures)


if __name__ == "__main__":
    sys.exit(main())
