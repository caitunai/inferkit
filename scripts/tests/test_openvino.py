"""Verify checksum, CPU-only extraction and failure cleanup without loading native code."""

import hashlib
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import zipfile


PROJECT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("prepare_openvino", PROJECT / "scripts/prepare-openvino-linux.py")
PACK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PACK)


class OpenVINOPackaging(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="inferkit-openvino-test-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.output = self.root / "runtime"
        self.archive = self.root / "runtime.whl"

    def test_bad_checksum_never_creates_output(self):
        self.archive.write_bytes(b"corrupt wheel")
        with self.assertRaisesRegex(ValueError, "SHA-256 mismatch"):
            PACK.package(self.output, self.archive)
        self.assertFalse(self.output.exists())
        self.assertFalse(Path(str(self.output) + ".prepare-lock").exists())

    def test_existing_output_is_preserved(self):
        self.output.mkdir()
        marker = self.output / "preserve"
        marker.write_text("keep")
        with self.assertRaises(ValueError):
            PACK.package(self.output)
        self.assertEqual(marker.read_text(), "keep")

    def test_existing_lock_is_not_removed(self):
        lock = Path(str(self.output) + ".prepare-lock")
        lock.mkdir()
        with self.assertRaises(FileExistsError):
            PACK.package(self.output)
        self.assertTrue(lock.exists())

    def test_wrong_elf_architecture_cleans_partial_staging(self):
        with zipfile.ZipFile(self.archive, "w") as archive:
            archive.writestr(PACK.PACKAGE + PACK.LIBRARIES[0], b"not an amd64 library")
        digest = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        with mock.patch.object(PACK, "SHA256", digest):
            with self.assertRaisesRegex(ValueError, "Linux amd64 ELF"):
                PACK.package(self.output, self.archive)
        self.assertFalse(self.output.exists())
        self.assertEqual(list(self.root.iterdir()), [self.archive])

    @unittest.skipUnless(os.environ.get("OPENVINO_TEST_ARCHIVE"), "set OPENVINO_TEST_ARCHIVE for official offline-wheel integration")
    def test_verified_package_contains_only_cpu_runtime(self):
        PACK.package(self.output, Path(os.environ["OPENVINO_TEST_ARCHIVE"]))
        names = {p.name for p in (self.output / "lib").iterdir()}
        self.assertEqual(names, set(PACK.LIBRARIES))
        self.assertFalse(any("gpu" in name or "npu" in name or "auto_" in name for name in names))
        self.assertTrue((self.output / "licenses/third-party-programs.txt").is_file())
        self.assertEqual(self.output.stat().st_mode & 0o777, 0o755)
        for library in (self.output / "lib").iterdir():
            self.assertEqual(library.stat().st_mode & 0o777, 0o644)


if __name__ == "__main__":
    unittest.main()
