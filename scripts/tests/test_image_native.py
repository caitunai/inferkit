"""Check native deployment guards without changing system libraries."""

from pathlib import Path
import subprocess
import tempfile
import unittest

PROJECT = Path(__file__).resolve().parents[2]


class NativeImageDeployment(unittest.TestCase):
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
