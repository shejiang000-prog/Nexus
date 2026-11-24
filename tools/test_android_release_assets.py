"""Check that both release APKs authorize the exact Linux assets being published."""
import hashlib
from pathlib import Path
import tempfile
import unittest
import zipfile

from prepare_android_release import prepare


class ReleaseAssetsTest(unittest.TestCase):
    def test_pins_match_external_payloads(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            changelog = root / "CHANGELOG.md"
            changelog.write_text("## 1.2.3\nTest release\n")
            nodes, dest = root / "nodes", root / "release"
            pins = {}
            for arch in ("x86_64", "aarch64"):
                directory = nodes / arch
                directory.mkdir(parents=True)
                payload = arch.encode()
                (directory / "nexus-node").write_bytes(payload)
                pins[f"assets/linux/{arch}/nexus-node.sha256"] = hashlib.sha256(payload).hexdigest()

            for suffix, abis in (("arm64", ["arm64-v8a"]), ("universal", ["arm64-v8a", "x86_64"])):
                with zipfile.ZipFile(root / f"Nexus-1.2.3-{suffix}.apk", "w") as apk:
                    for abi in abis:
                        for lib in ("libnexus_android.so", "libshoes.so", "libgojni.so"):
                            apk.writestr(f"lib/{abi}/{lib}", b"stub")
                    for name, digest in pins.items():
                        apk.writestr(name, digest)

            prepare("v1.2.3", root, nodes, dest, changelog)
            for arch, cpu in (("x86_64", "amd64"), ("aarch64", "arm64")):
                self.assertEqual((dest / f"nexus-node-linux-{cpu}").read_bytes(), (nodes / arch / "nexus-node").read_bytes())

            source = nodes / "x86_64" / "nexus-node"
            original = source.read_bytes()
            source.write_bytes(original + b"changed")
            with self.assertRaisesRegex(ValueError, "matching Linux binary digest"):
                prepare("v1.2.3", root, nodes, dest, changelog)
            source.write_bytes(original)
            with zipfile.ZipFile(root / "Nexus-1.2.3-arm64.apk", "a") as apk:
                apk.writestr("assets/linux/x86_64/nexus-node", original)
            with self.assertRaisesRegex(ValueError, "matching Linux binary digest"):
                prepare("v1.2.3", root, nodes, dest, changelog)


if __name__ == "__main__":
    unittest.main()
