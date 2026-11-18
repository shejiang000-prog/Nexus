"""Build Rust JNI libraries with the Android NDK selected by the Gradle project."""
import os
import shutil
import subprocess
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[1]
ndk = Path(sys.argv[1])
out = Path(sys.argv[2]).resolve()
host = {"win32":"windows-x86_64", "linux":"linux-x86_64", "darwin":"darwin-x86_64"}[sys.platform]
toolchain = ndk / "toolchains/llvm/prebuilt" / host / "bin"
targets = {"arm64-v8a":"aarch64-linux-android", "x86_64":"x86_64-linux-android"}
cargo = shutil.which("cargo") or str(Path.home() / ".cargo/bin" / ("cargo.exe" if os.name == "nt" else "cargo"))
for abi, target in targets.items():
    clang = toolchain / (target + "26-clang" + (".cmd" if os.name == "nt" else ""))
    ar = toolchain / ("llvm-ar.exe" if os.name == "nt" else "llvm-ar")
    env = dict(os.environ)
    env["CC_" + target] = str(clang)
    env["AR_" + target] = str(ar)
    env["CARGO_TARGET_" + target.upper().replace("-", "_") + "_LINKER"] = str(clang)
    target_dir = root / "target/android"
    env["CARGO_TARGET_DIR"] = str(target_dir)
    subprocess.run([cargo, "build", "--locked", "--release", "--target", target, "-p", "nexus-android"],
                   cwd=root, env=env, check=True)
    dest = out / abi
    dest.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(target_dir / target / "release/libnexus_android.so", dest / "libnexus_android.so")
