"""Exercise shared ONNX packaging and C API guards without changing system libraries."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


PROJECT = Path(__file__).resolve().parents[2]


class RuntimePackaging(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="inferkit-runtime-test-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.environment = os.environ.copy()
        self.environment["PATH"] = str(self.bin) + os.pathsep + os.environ["PATH"]

    def command(self, name, body):
        path = self.bin / name
        path.write_text("#!/bin/sh\nset -eu\n" + body + "\n")
        path.chmod(0o755)

    def run_script(self, name, *arguments):
        return subprocess.run(["bash", str(PROJECT / "scripts" / name), *map(str, arguments)],
                              env=self.environment, capture_output=True, text=True, check=False)

    def test_corrupt_archive_never_creates_output_and_cleans_its_lock(self):
        output = self.root / "onnxruntime"
        archive = self.root / "corrupt.tgz"
        archive.write_bytes(b"not the official release")
        result = self.run_script("prepare-onnxruntime-linux.sh", output, archive)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("SHA-256 mismatch", result.stderr)
        self.assertFalse(output.exists())
        self.assertFalse(Path(str(output) + ".prepare-lock").exists())

    def test_existing_output_is_preserved(self):
        output = self.root / "onnxruntime"
        output.mkdir()
        marker = output / "existing"
        marker.write_text("preserve me")
        result = self.run_script("prepare-onnxruntime-linux.sh", output)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(marker.read_text(), "preserve me")

    def api_headers(self, requested, provided):
        binding = self.root / "binding"
        binding.mkdir()
        (binding / "onnxruntime_c_api.h").write_text(f"#define ORT_API_VERSION {requested}\n")
        runtime = self.root / "onnxruntime"
        (runtime / "include").mkdir(parents=True)
        (runtime / "include/onnxruntime_c_api.h").write_text(f"#define ORT_API_VERSION {provided}\n")
        self.command("go", 'printf "%s\\n" "$TEST_BINDING"')
        self.environment["TEST_BINDING"] = str(binding)
        return runtime

    def test_matching_runtime_api_is_accepted(self):
        runtime = self.api_headers(31, 31)
        result = self.run_script("check-onnxruntime-api.sh", runtime)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("C API 31 matches", result.stdout)

    def test_newer_binding_cannot_silently_use_an_older_runtime(self):
        runtime = self.api_headers(32, 31)
        result = self.run_script("check-onnxruntime-api.sh", runtime)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("requests 32", result.stderr)
        self.assertIn("provides 31", result.stderr)

    def test_invalid_api_headers_do_not_compare_as_empty_versions(self):
        runtime = self.api_headers("", "")
        result = self.run_script("check-onnxruntime-api.sh", runtime)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid", result.stderr)

    @unittest.skipUnless(os.environ.get("ONNXRUNTIME_TEST_ARCHIVE"), "set ONNXRUNTIME_TEST_ARCHIVE for official offline-package integration")
    def test_verified_offline_package_preserves_versioned_library_and_symlinks(self):
        output = self.root / "onnxruntime"
        result = self.run_script("prepare-onnxruntime-linux.sh", output, os.environ["ONNXRUNTIME_TEST_ARCHIVE"])
        self.assertEqual(result.returncode, 0, result.stderr)
        library = output / "lib/libonnxruntime.so.1.31.1"
        self.assertTrue(library.is_file())
        self.assertTrue((output / "lib/libonnxruntime.so").is_symlink())
        self.assertEqual((output / "lib/libonnxruntime.so").resolve(), library.resolve())


if __name__ == "__main__":
    unittest.main()
