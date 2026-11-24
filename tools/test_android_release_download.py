"""Exercise the Android SSH download script with offline curl and GNU Wget fixtures."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ASSET = Path(__file__).resolve().parents[1] / "apps/android/app/src/main/assets/download_node.sh"
START = "https://release.test/tag/node"


@unittest.skipUnless(os.name == "posix", "the download runs on Linux")
class ReleaseDownloadTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.log = self.root / "calls"
        self.dest = self.root / "node"
        (self.root / "awk").symlink_to(shutil.which("awk"))
        self.stub("wget", """
for arg do
    case "$arg" in --output-document=*) dest=${arg#*=} ;; esac
    url=$arg
done
printf '%s\\n' "$*" >> "$CALLS"
case "$url" in
    */tag/node) printf '  HTTP/1.1 302 Found\\r\\n  Location: %s\\r\\n' "$LOCATION" >&2; exit 8 ;;
    */notfound) echo '  HTTP/1.1 404 Not Found' >&2; exit 8 ;;
    */partial) echo '  HTTP/1.1 200 OK' >&2; exit 4 ;;
    *) echo '  HTTP/1.1 200 OK' >&2; printf payload > "$dest" ;;
esac
""")

    def stub(self, name, body):
        executable = self.root / name
        executable.write_text("#!/bin/sh\n" + body)
        executable.chmod(0o700)

    def fetch(self, location="", curl_status=0):
        self.log.write_text("")
        result = subprocess.run(["/bin/sh", "-s", "--", START, self.dest], input=ASSET.read_text(),
                                text=True, capture_output=True,
                                env=dict(PATH=str(self.root), CALLS=str(self.log), LOCATION=location,
                                         CURL_STATUS=str(curl_status)), timeout=5)
        return result.returncode, self.log.read_text().splitlines()

    def test_curl_preferred_and_errors_final(self):
        self.stub("curl", 'echo "$*" >> "$CALLS"; exit "$CURL_STATUS"\n')
        for status in (0, 22):
            with self.subTest(status=status):
                result, calls = self.fetch(curl_status=status)
                self.assertEqual(result, status)
                self.assertEqual(calls, [f"-q --proto =https --proto-redir =https --fail --silent --show-error "
                                         f"--location --connect-timeout 15 --max-time 300 --output {self.dest} -- {START}"])

    def test_wget_checks_each_redirect_before_request(self):
        for location, expected in [("https://assets.test/bin", "https://assets.test/bin"),
                                   ("/bin", "https://release.test/bin"), ("bin", "https://release.test/tag/bin")]:
            with self.subTest(location=location):
                result, calls = self.fetch(location)
                self.assertEqual(result, 0)
                self.assertEqual(self.dest.read_bytes(), b"payload")
                self.assertEqual([line.split()[-1] for line in calls], [START, expected])
                for line in calls:
                    self.assertIn("--max-redirect=0", line)
                    self.assertIn("--connect-timeout=15", line)
                    self.assertNotIn("--no-check-certificate", line)
        for location, count in [("http://assets.test/bin", 1), ("/notfound", 2), ("/partial", 2)]:
            with self.subTest(location=location):
                result, calls = self.fetch(location)
                self.assertNotEqual(result, 0)
                self.assertEqual(len(calls), count)


if __name__ == "__main__":
    unittest.main()
