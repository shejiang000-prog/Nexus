"""Fetch the pinned upstream Android AAR and verify the release archive before extraction."""
import hashlib
import os
from pathlib import Path
import shutil
import sys
import tempfile
import urllib.request
import zipfile

VERSION = "v26.11.9"
SHA256 = "1b1aa12124a7112af983a6cea74c77f27aadc6b650acf3c5a57019619ba9fa48"
URL = f"https://github.com/XTLS/libXray/releases/download/{VERSION}/libxray-android.zip"

def fetch(dest):
    dest.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=dest.parent) as temp:
        archive = Path(temp)/"download.zip"
        local = os.environ.get("NEXUS_LIBXRAY_ARCHIVE")
        if local:
            shutil.copyfile(local,archive)
        else:
            with urllib.request.urlopen(URL, timeout=60) as response, archive.open("wb") as output:
                shutil.copyfileobj(response,output)
        with archive.open("rb") as f:
            digest=hashlib.sha256()
            for chunk in iter(lambda: f.read(1024*1024),b""):
                digest.update(chunk)
        if digest.hexdigest()!=SHA256:
            raise ValueError("libXray release checksum mismatch")
        aar = Path(temp)/"libXray.aar"
        with zipfile.ZipFile(archive) as z, z.open("libxray-android/libXray.aar") as source, aar.open("wb") as out:
            shutil.copyfileobj(source,out)
        os.replace(aar,dest)

if __name__=="__main__":
    fetch(Path(sys.argv[1]))
