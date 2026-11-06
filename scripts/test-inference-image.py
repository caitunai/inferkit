#!/usr/bin/env python3
"""Exercise the built image with pinned public OCR models, never private weights."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import uuid


PROJECT = Path(__file__).resolve().parents[1]
FORMATS = ("png", "jpeg", "webp", "avif")


class VerificationError(ValueError):
    """The runtime returned an incomplete or incompatible inference result."""


def configuration(models):
    settings = [
        'mode="release"', '[logger]', 'level="info"', '[image.ocr]',
        'modelSize="small"', 'provider="cpu"', 'detectionProvider="cpu"',
        'recognitionProvider="cpu"', 'warmupRuns=3', 'coremlCache=true',
        'recognitionBatchSize=4', 'library="/opt/onnxruntime/lib/libonnxruntime.so.1"',
    ]
    for name, filename in (("detection", "det.onnx"), ("recognition", "rec.onnx"), ("dictionary", "dict.txt")):
        digest = hashlib.sha256()
        with (models / filename).open("rb") as source:
            for chunk in iter(lambda: source.read(1 << 20), b""):
                digest.update(chunk)
        key = name if name == "dictionary" else name + "Model"
        settings.extend([f'{key}="/models/ocr/{filename}"', f'{name}SHA256="{digest.hexdigest()}"'])
    return "\n".join(settings) + "\n"


def check(condition, message):
    if not condition:
        raise VerificationError(message)


def log_events(stderr):
    events = []
    for line in stderr.splitlines():
        try:
            value = json.loads(line)
        except json.JSONDecodeError:
            continue  # Native library diagnostics may precede structured logs.
        if isinstance(value, dict):
            events.append(value)
    return events


def verify_output(stdout, stderr):
    results = [json.loads(line) for line in stdout.splitlines() if line.strip()]
    events = log_events(stderr)
    timings = [event for event in events if event.get("message") == "OCR timing"]
    check(len(results) == len(timings) == 6, "expected six results and six OCR summaries")
    check(results[0]["text"] == results[1]["text"], "buffer reuse changed OCR text")
    for result in results[:2]:
        check("OCR" in result["text"] and len(result["lines"]) >= 8, "synthetic text was not recognized")
        check((result["width"], result["height"]) == (320, 640), "text fixture dimensions changed")
    for result in results[2:]:
        check((result["width"], result["height"]) == (48, 32), "four-format fixture dimensions changed")
    verify_timings(timings)
    verify_startup(events)
    return {
        "formats": list(FORMATS), "text_lines": len(results[0]["lines"]),
        "batches": timings[0]["recognition_batches"], "batch_max": timings[0]["recognition_batch_max"],
        "reused_slots": timings[1]["buffer_reused"], "tensor_backend": timings[0]["tensor_backend"],
    }


def verify_timings(timings):
    expected = ("png", "png") + FORMATS
    for timing, format_name in zip(timings, expected):
        check(timing.get("success") is True and not timing.get("failed_stage"), "OCR stage failed")
        check(timing.get("image_format") == format_name, "wrong decoded image format")
        check(timing.get("detection_provider") == timing.get("recognition_provider") == "cpu", "CPU provider selection ignored")
        check(timing.get("tensor_backend") in ("go_simd", "go"), "unknown OCR kernel backend")
        check(timing.get("detection_inference_ms", 0) > 0, "missing real detection inference")
        if format_name in ("jpeg", "webp"):
            check(timing.get("decode_backend") == "libvips", "native JPEG/WebP decoder inactive")
    for timing in timings[:2]:
        check(timing.get("recognition_inference_sum_ms", 0) > 0 and timing.get("ctc_sum_ms", 0) > 0, "missing recognition/CTC timing")
        check(timing.get("recognition_batch_max", 0) >= 2, "real batched recognition inactive")
        check(0 < timing.get("recognition_batches", 0) < timing.get("recognition_lines", 0), "line grouping inactive")
    check(timings[1].get("buffer_reused", 0) > 0, "repeated image did not reuse buffers")


def verify_startup(events):
    warmups = [event for event in events if event.get("message") == "OCR startup warmup complete"]
    check(len(warmups) == 1 and warmups[0].get("warmup_runs") == 3, "finite startup warmup inactive")
    models = [event for event in events if event.get("message") == "OCR model initialized"]
    check(len(models) == 2 and {event.get("stage") for event in models} == {"detection", "recognition"}, "missing model startup summaries")
    for model in models:
        check(model.get("execution_provider") == "cpu" and model.get("coreml_cache") is False, "CPU image attempted CoreML")


def container_command(image, models, config, name):
    mounts = ((models, "/models/ocr"), (config, "/etc/inferkit/ocr-test.toml"),
              (PROJECT / "services/ocr/testdata", "/fixtures/text"),
              (PROJECT / "services/imagefilter/testdata", "/fixtures/formats"))
    args = ["docker", "run", "--rm", "--name", name, "--pull=never", "--network=none", "--read-only",
            "--user=10001:10001", "--cpus=2", "--memory=4g", "--cap-drop=ALL",
            "--security-opt=no-new-privileges:true", "--tmpfs", "/tmp:size=128m,mode=1777"]
    for source, destination in mounts:
        args.extend(["--mount", f"type=bind,source={source},target={destination},readonly"])
    args.extend([image, "--config", "/etc/inferkit/ocr-test.toml", "ocr", "/fixtures/text/ci.png", "/fixtures/text/ci.png"])
    args.extend(f"/fixtures/formats/sample.{name}" for name in FORMATS)
    return args


def run_container(image, models, config):
    name = "inferkit-check-" + uuid.uuid4().hex
    try:
        return subprocess.run(container_command(image, models, config, name), capture_output=True, text=True, timeout=180)
    finally:
        # Killing a timed-out Docker CLI does not stop its container. Always
        # release this check's uniquely named container, including on timeout.
        cleanup = subprocess.run(["docker", "rm", "--force", name], capture_output=True, text=True, timeout=15)
        if cleanup.returncode and "No such container" not in cleanup.stderr:
            raise VerificationError("cannot remove smoke-test container: " + cleanup.stderr)


def run(image, supplied_models):
    with tempfile.TemporaryDirectory(prefix="inferkit-image-check-") as directory:
        work = Path(directory)
        models = supplied_models
        if models is None:
            models = work / "models"
            subprocess.run(["bash", str(PROJECT / "scripts/prepare-ocr-models.sh"), "--size", "small", str(models)], check=True, timeout=900)
        config = work / "ocr-test.toml"
        config.write_text(configuration(models), encoding="utf-8")
        config.chmod(0o444)  # The config has no secrets; the image's UID must read it.
        result = run_container(image, models, config)
        if result.returncode:
            sys.stderr.write(result.stderr)
            raise VerificationError("container OCR command failed")
        try:
            report = verify_output(result.stdout, result.stderr)
        except (KeyError, TypeError, ValueError):
            sys.stderr.write(result.stderr)
            raise
        print("Inference image verified: " + json.dumps(report, sort_keys=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("image", help="already-built Debian 13 amd64 image")
    parser.add_argument("--models", type=lambda value: Path(value).resolve(), help="prepared small model directory; skip downloading")
    args = parser.parse_args()
    if args.image.startswith("-"):
        parser.error("invalid image name")
    run(args.image, args.models)


if __name__ == "__main__":
    try:
        main()
    except (OSError, subprocess.SubprocessError, KeyError, TypeError, ValueError) as error:
        sys.exit("Inference image verification failed: " + str(error))
