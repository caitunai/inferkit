"""Check native deployment guards without changing system libraries."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest

PROJECT = Path(__file__).resolve().parents[2]


class NativeImageDeployment(unittest.TestCase):
    def test_missing_decompressor_fails_before_creating_output(self):
        for missing in ("xz", "gzip"):
            with self.subTest(missing=missing):
                self.assert_missing_decompressor(missing)

    def assert_missing_decompressor(self, missing):
        with tempfile.TemporaryDirectory(prefix="inferkit-native-tools-") as directory:
            root = Path(directory)
            tools = root / "bin"
            tools.mkdir()
            uname = tools / "uname"
            uname.write_text('#!/bin/sh\ncase "$1" in -s) printf "Linux\\n";; -m) printf "x86_64\\n";; *) exit 1;; esac\n')
            uname.chmod(0o755)
            if missing == "gzip":
                xz = tools / "xz"
                xz.write_text("#!/bin/sh\nexit 99\n")
                xz.chmod(0o755)
            environment = os.environ.copy()
            environment["PATH"] = str(tools)
            output = root / "image"
            result = subprocess.run(["/bin/bash", str(PROJECT / "scripts/prepare-image-linux.sh"), str(output)],
                                    env=environment, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Missing command: " + missing, result.stderr)
            self.assertFalse(output.exists())
            self.assertEqual(sorted(path.name for path in root.iterdir()), ["bin"])

    def test_native_dependency_builder_rejects_an_unsupported_host(self):
        if subprocess.check_output(["uname", "-s"], text=True).strip() == "Linux":
            self.skipTest("host rejection is tested from non-Linux hosts")
        with tempfile.TemporaryDirectory(prefix="inferkit-native-guard-") as directory:
            output = Path(directory) / "image"
            result = subprocess.run(["bash", str(PROJECT / "scripts/prepare-image-linux.sh"), str(output)], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output.exists())
            self.assertIn("Linux x86_64", result.stderr)


if __name__ == "__main__":
    unittest.main()
