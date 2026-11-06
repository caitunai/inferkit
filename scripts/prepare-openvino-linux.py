#!/usr/bin/env python3
"""Package pinned Linux amd64 OpenVINO CPU libraries without installing Python at runtime."""

import argparse
import hashlib
from pathlib import Path
import shutil
import tempfile
import urllib.request
import zipfile


VERSION = "1.8.1"
SHA256 = "3832643158c8c187fcab6db661cde2a8c06a2b3559d95d0f2411ac0e51c5e924"
URL = ("https://files.pythonhosted.org/packages/88/01/"
       "4e72a8a65b584f01d6e59eb453bae397b82b2e51df1b5c94efa05e17800b/"
       "onnxruntime_ep_openvino-1.8.1-py3-none-manylinux_2_28_x86_64.whl")
PACKAGE = "onnxruntime_ep_openvino/"
# An explicit CPU-only allowlist also makes the plugin's sole physical device
# unambiguous to the Go binding, which does not expose OrtEpDevice metadata.
LIBRARIES = (
    "libonnxruntime_providers_openvino_plugin.so",
    "libopenvino.so.2641", "libopenvino_intel_cpu_plugin.so",
    "libopenvino_ir_frontend.so.2641", "libopenvino_onnx_frontend.so.2641",
    "libtbb.so.12", "libtbbbind_2_5.so.3", "libtbbmalloc.so.2",
    "libtbbmalloc_proxy.so.2", "libhwloc.so.15",
)


def copy_member(archive, member, destination, elf=False):
    info = archive.getinfo(member)
    if info.file_size > 256 << 20:
        raise ValueError("unexpected OpenVINO member size: " + member)
    with archive.open(info) as source, destination.open("xb") as target:
        if elf:
            header = source.read(20)
            if header[:6] != b"\x7fELF\x02\x01" or header[18:20] != b"\x3e\x00":
                raise ValueError("expected Linux amd64 ELF: " + member)
            target.write(header)
        shutil.copyfileobj(source, target)
    destination.chmod(0o644)


def package(output, supplied_archive=None):
    output = output.absolute()
    if output.exists() or output.is_symlink():
        raise ValueError("output already exists: " + str(output))
    output.parent.mkdir(parents=True, exist_ok=True)
    lock = Path(str(output) + ".prepare-lock")
    lock.mkdir()  # Do not remove another preparation's lock on failure.
    try:
        with tempfile.TemporaryDirectory(prefix="openvino-", dir=output.parent) as directory:
            work = Path(directory)
            wheel = work / "openvino.whl"
            if supplied_archive:
                shutil.copyfile(supplied_archive, wheel)
            else:
                with urllib.request.urlopen(URL, timeout=180) as response, wheel.open("xb") as target:
                    shutil.copyfileobj(response, target)
            with wheel.open("rb") as source:
                digest = hashlib.file_digest(source, "sha256").hexdigest()
            if digest != SHA256:
                raise ValueError("OpenVINO wheel SHA-256 mismatch")
            stage = work / "runtime"
            libraries = stage / "lib"
            licenses = stage / "licenses"
            libraries.mkdir(parents=True)
            licenses.mkdir()
            with zipfile.ZipFile(wheel) as archive:
                if len(archive.namelist()) != len(set(archive.namelist())):
                    raise ValueError("duplicate OpenVINO archive members")
                for name in LIBRARIES:
                    copy_member(archive, PACKAGE + name, libraries / name, elf=True)
                copy_member(archive, PACKAGE + "third-party-programs.txt", licenses / "third-party-programs.txt")
                copy_member(archive, f"onnxruntime_ep_openvino-{VERSION}.dist-info/METADATA", licenses / "METADATA")
            (stage / "VERSION").write_text(VERSION + "\n", encoding="utf-8")
            # TemporaryDirectory uses 0700; the runtime itself must be readable
            # by the final non-root container account.
            for directory in (stage, libraries, licenses):
                directory.chmod(0o755)
            stage.rename(output)
    finally:
        lock.rmdir()
    print(f"OpenVINO EP {VERSION} CPU-only runtime prepared: {output}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("--archive", type=Path, help="use the same verified wheel offline")
    args = parser.parse_args()
    try:
        package(args.output, args.archive)
    except (OSError, ValueError, KeyError, zipfile.BadZipFile) as error:
        parser.exit(1, str(error) + "\n")


if __name__ == "__main__":
    main()
