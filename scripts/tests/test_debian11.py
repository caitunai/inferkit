"""Exercise packaging failures and ABI gates without changing system libraries."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


PROJECT = Path(__file__).resolve().parents[2]


class DeploymentScripts(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="inferkit-debian11-test-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.elf = self.root / "runtime.so"
        self.elf.write_bytes(b"test fixture")
        self.versions = self.root / "versions.txt"
        self.environment = os.environ.copy()
        self.environment.update(PATH=str(self.bin) + os.pathsep + os.environ["PATH"], TEST_VERSIONS=str(self.versions))
        self.command("uname", 'if [ "$1" = -s ]; then printf "%s\\n" "${TEST_OS:-Linux}"; else printf "%s\\n" "${TEST_ARCH:-x86_64}"; fi')
        self.command("readelf", 'if [ "$1" = --file-header ]; then printf "Class: ELF64\\nMachine: ${TEST_MACHINE:-Advanced Micro Devices X86-64}\\n"; else cat "$TEST_VERSIONS"; fi')
        self.command("ldd", 'printf "%s\\n" "${TEST_LDD:-libc.so.6 => /lib/x86_64-linux-gnu/libc.so.6}"; exit "${TEST_LDD_STATUS:-0}"')

    def command(self, name, body):
        path = self.bin / name
        path.write_text("#!/bin/sh\nset -eu\n" + body + "\n")
        path.chmod(0o755)

    def run_script(self, name, *arguments):
        return subprocess.run(["bash", str(PROJECT / "scripts" / name), *map(str, arguments)],
                              env=self.environment, capture_output=True, text=True, check=False)

    def require_versions(self, *versions):
        self.versions.write_text("Version definition section '.gnu.version_d':\n  Name: GLIBC_2.99\n"
                                 "Version needs section '.gnu.version_r':\n" +
                                 "".join("  0x0000: Name: " + value + " Flags: none Version: 2\n" for value in versions))

    def test_accepts_compatible_requirements_and_ignores_exported_definitions(self):
        self.require_versions("GLIBC_2.9", "GLIBC_2.28", "GLIBCXX_3.4.22", "CXXABI_1.3.11")
        result = self.run_script("check-debian11-abi.sh", self.elf)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("GLIBC_2.28", result.stdout)
        self.assertNotIn("GLIBC_2.99", result.stdout)

    def test_rejects_symbols_newer_than_debian11(self):
        for version in ("GLIBC_2.34", "GLIBCXX_3.4.30", "CXXABI_1.3.13"):
            with self.subTest(version=version):
                self.require_versions(version)
                result = self.run_script("check-debian11-abi.sh", self.elf)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(version, result.stderr)

    def test_rejects_unresolved_dependencies_even_when_ldd_exits_zero(self):
        self.require_versions("GLIBC_2.28")
        self.environment["TEST_LDD"] = "libstdc++.so.6 => not found"
        result = self.run_script("check-debian11-abi.sh", self.elf)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not found", result.stderr)

    def test_preserves_loader_failures(self):
        self.require_versions("GLIBC_2.28")
        self.environment.update(TEST_LDD="loader failed", TEST_LDD_STATUS="1")
        result = self.run_script("check-debian11-abi.sh", self.elf)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("loader failed", result.stderr)

    def test_rejects_wrong_elf_architecture(self):
        self.environment["TEST_MACHINE"] = "AArch64"
        result = self.run_script("check-debian11-abi.sh", self.elf)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("x86_64 ELF64", result.stderr)

    def test_rejects_wrong_build_host_before_producing_artifacts(self):
        self.environment["TEST_OS"] = "Darwin"
        output = self.root / "release"
        result = self.run_script("build-debian11.sh", output)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Debian 11", result.stderr)
        self.assertFalse(output.exists())

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
