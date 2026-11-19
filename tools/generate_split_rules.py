"""Build the Android routing snapshot from checksum-pinned release inputs."""
import argparse
import hashlib
import ipaddress
import json
from pathlib import Path
import urllib.request

DOMAIN_VERSION = "20261118141523"
IP_VERSION = "be3de75a9698c50193c35c1215509e89d5d5cf6f"
IP_URL = f"https://raw.githubusercontent.com/gaoyifan/china-operator-ip/{IP_VERSION}"
SOURCES = {
    "domains.yml": (f"https://github.com/v2fly/domain-list-community/releases/download/{DOMAIN_VERSION}/dlc.dat_plain.yml",
                    "20352b9263f6e3db420465efb565a7a0345ddde535afedf92515a7bb0ffa4dd8"),
    "china.txt": (f"{IP_URL}/china.txt", "e2c280f8adfd3a5f16e0cb5e518193b7f4487f010b5e92ac7b6513317904bb8f"),
    "china6.txt": (f"{IP_URL}/china6.txt", "225a126c2b779d3962861558b6b1435d667da189b036bbdfb31f544974356c59"),
}
# Complement of the Internet destinations admitted by nexus-server::Policy.
PRIVATE = """0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12
192.168.0.0/16 192.0.0.0/24 192.0.2.0/24 192.88.99.0/24 198.18.0.0/15 198.51.100.0/24
203.0.113.0/24 168.63.129.16/32 224.0.0.0/3 ::/3 4000::/2 8000::/1 2002::/16
2001::/23 2001:db8::/32 3fff::/20""".split()

def source(cache, name):
    url, digest = SOURCES[name]
    path = cache / name
    data = path.read_bytes() if path.exists() else urllib.request.urlopen(url, timeout=60).read()
    if hashlib.sha256(data).hexdigest() != digest:
        raise ValueError(f"{name}: checksum mismatch")
    if not path.exists():
        path.write_bytes(data)
    return data.decode("utf8")

def generate(cache, output):
    cache.mkdir(parents=True, exist_ok=True)
    block = source(cache, "domains.yml").split('  - name: "cn"\n', 1)[1].split('  - name:', 1)[0]
    rules = [json.loads(line.strip()[2:]).split(":@", 1)[0] for line in block.splitlines() if line.startswith("      - ")]
    if f"    length: {len(rules)}" not in block.splitlines() or not rules:
        raise ValueError("incomplete CN list")
    networks = []
    for file in ("china.txt", "china6.txt"):
        networks.extend(source(cache, file).splitlines())
    networks = [ipaddress.ip_network(x) for x in networks + PRIVATE]
    lines = sorted(set(rules))
    for version in (4, 6):
        lines.extend("ip:" + str(n) for n in ipaddress.collapse_addresses(n for n in networks if n.version == version))
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_bytes(("\n".join(lines) + "\n").encode())

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("cache", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    generate(args.cache, args.output)
