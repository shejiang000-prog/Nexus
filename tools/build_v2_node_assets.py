"""Build the first-party Linux installer that the Android SSH flow uploads."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from prepare_v2 import V2, prepare


def main(output: Path):
    with tempfile.TemporaryDirectory(prefix="nexus-node-assets-") as work:
        patched = Path(work).resolve() / "mod"
        prepare(patched, False)
        for arch, directory in (("amd64", "x86_64"), ("arm64", "aarch64")):
            target = output / directory / "nexus-node"
            target.parent.mkdir(parents=True, exist_ok=True)
            subprocess.run(
                ["go", "build", "-trimpath", "-buildvcs=false", "-mod=readonly",
                 f"-modfile={patched / 'v2.mod'}", "-ldflags=-s -w", "-o", target,
                 "./cmd/nexus-node"],
                cwd=V2, check=True,
                env={**os.environ, "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": arch},
            )


if __name__ == "__main__":
    main(Path(sys.argv[1]).resolve())

