"""Verify both pinned inputs and deterministic output. Pass the Gradle download cache to work offline."""
import argparse
from pathlib import Path
import tempfile
from unittest.mock import patch

from generate_split_rules import generate, source

def verify(cache):
    with tempfile.TemporaryDirectory() as temp:
        root = Path(temp)
        first, second = root/"first.txt", root/"second.txt"
        generate(cache, first)
        generate(cache, second)
        assert first.read_bytes() == second.read_bytes()
        print(f"PASS reproducible snapshot ({first.stat().st_size} bytes)")
        (root/"china.txt").write_bytes(b"192.0.2.0/24\n")
        with patch("urllib.request.urlopen", side_effect=AssertionError("unexpected fetch")):
            try:
                source(root,"china.txt")
            except ValueError as err:
                assert "checksum mismatch" in str(err)
            else:
                raise AssertionError("accepted corrupted source")
        print("PASS checksum mismatch rejected")

if __name__ == "__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("cache",type=Path)
    verify(p.parse_args().cache)
