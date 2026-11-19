"""Connect every compatibility node, including structured and transport variants, from an LDPlayer
emulator through the app's own conversion to disposable WSL peers, and require a node-side receipt."""
import argparse
import re
import subprocess
import sys

PACKAGE = "dev.nexus.localclient"
RUNNER = PACKAGE + ".test/" + PACKAGE + ".CatalogCheck"


def main(args):
    if not re.fullmatch(r"emulator-\d+", args.serial):
        raise SystemExit("an emulator serial is required")
    adb = ["adb", "-s", args.serial]
    wsl = ["wsl", "-d", args.distro, "--"]
    fixture = subprocess.check_output(wsl + ["mktemp", "-u", "/tmp/nexus-interop-XXXXXX.json"], text=True).strip()
    command = wsl + ["python3", "-u", "tools/compat_lab.py", "--host", args.host, "--xray", args.xray,
                     "--fixture", fixture, "--variants"]
    if args.shoes:
        command += ["--shoes", args.shoes]
    lab = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True,
                           encoding="utf8", errors="replace")
    try:
        for line in lab.stdout:
            if "fixtures ready" in line:
                print(line.strip(), flush=True)
                break
        else:
            raise SystemExit("fixture exited before it was ready")
        data = subprocess.check_output(wsl + ["cat", fixture])
        subprocess.run(adb + ["shell", "run-as", PACKAGE, "sh", "-c", "'cat > no_backup/qa-interop'"],
                       input=data, check=True, timeout=20)
        output = subprocess.run(adb + ["shell", "am", "instrument", "-w", "-e", "mode", "interop", RUNNER],
                                capture_output=True, text=True, encoding="utf8", errors="replace", timeout=600).stdout
        print(output.strip(), flush=True)
        return 0 if "INSTRUMENTATION_CODE: -1" in output else 1
    finally:
        subprocess.run(adb + ["shell", "run-as", PACKAGE, "rm", "-f", "no_backup/qa-interop"], check=False)
        if lab.poll() is None:
            lab.stdin.write("\n")
            lab.stdin.flush()
        lab.wait(20)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--serial", required=True)
    parser.add_argument("--distro", default="NexusLab")
    parser.add_argument("--host", required=True, help="WSL address the emulator can reach")
    parser.add_argument("--xray", required=True, help="Linux Xray path inside WSL")
    parser.add_argument("--shoes", help="Linux shoes path inside WSL; adds TUIC and AnyTLS")
    sys.exit(main(parser.parse_args()))
