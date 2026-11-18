"""Build the pinned TUIC / AnyTLS JNI library separately from the Nexus Rust workspace."""
import os
from pathlib import Path
import shutil
import subprocess
import sys

REVISION = "7680938b96a245a21d9e481ad009205b956e4436"
root = Path(__file__).resolve().parents[1]
source = Path(os.environ.get("NEXUS_SHOES_SRC",root/"target"/"shoes-source"))
target = Path(os.environ.get("NEXUS_SHOES_TARGET",root/"target"/"shoes-android")).resolve()
if not source.exists():
    source.parent.mkdir(parents=True,exist_ok=True)
    subprocess.run(["git","clone","--depth","1","--branch","v0.3.8","https://github.com/cfal/shoes.git",str(source)],check=True)
if subprocess.check_output(["git","rev-parse","HEAD"],cwd=source,text=True).strip()!=REVISION:
    raise ValueError("unexpected shoes source revision")
subprocess.run(["git","diff","--quiet","HEAD"],cwd=source,check=True)
ndk, out, notices = map(Path,sys.argv[1:])
env = dict(os.environ,ANDROID_NDK_HOME=str(ndk),CARGO_TARGET_DIR=str(target))
subprocess.run(["cargo","ndk","-t","x86_64","-t","arm64-v8a","-P","26","--","build","--release","--lib","--locked"],
               cwd=source,env=env,check=True)
for abi,triple in [("arm64-v8a","aarch64-linux-android"),("x86_64","x86_64-linux-android")]:
    dest = out/abi
    dest.mkdir(parents=True,exist_ok=True)
    shutil.copyfile(target/triple/"release/libshoes.so",dest/"libshoes.so")
dest=notices/"licenses/shoes"
dest.mkdir(parents=True,exist_ok=True)
for name in ("LICENSE","THIRD_PARTY_NOTICES.md"):
    shutil.copyfile(source/name,dest/name)
shutil.copytree(source/"licenses",dest/"licenses",dirs_exist_ok=True)
