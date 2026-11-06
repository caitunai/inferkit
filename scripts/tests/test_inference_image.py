"""Reject incomplete release checks without requiring Docker or model downloads."""

import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


PROJECT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("inference_image", PROJECT / "scripts/test-inference-image.py")
SMOKE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SMOKE)


def valid_records():
    results, timings = [], []
    for index, format_name in enumerate(("png", "png") + SMOKE.FORMATS):
        result = {"width": 48, "height": 32, "text": "", "lines": []}
        if index < 2:
            result.update(width=320, height=640, text="OCR", lines=[{}] * 12)
        results.append(result)
        timings.append({
            "message": "OCR timing", "success": True, "failed_stage": "",
            "image_format": format_name, "decode_backend": "libvips",
            "detection_provider": "cpu", "recognition_provider": "cpu", "tensor_backend": "go_simd",
            "detection_inference_ms": 1, "recognition_inference_sum_ms": 1, "ctc_sum_ms": 1,
            "recognition_batch_max": 4, "recognition_batches": 3, "recognition_lines": 12, "buffer_reused": 2,
        })
    startup = [
        {"message": "OCR model initialized", "stage": stage, "execution_provider": "cpu", "coreml_cache": False}
        for stage in ("detection", "recognition")
    ]
    startup.append({"message": "OCR startup warmup complete", "warmup_runs": 3})
    return results, startup + timings


def serialize(records):
    return "\n".join(json.dumps(record) for record in records)


class InferenceImageVerification(unittest.TestCase):
    def test_complete_real_inference_contract(self):
        results, events = valid_records()
        report = SMOKE.verify_output(serialize(results), serialize(events))
        self.assertEqual(report["text_lines"], 12)
        self.assertEqual(report["formats"], ["png", "jpeg", "webp", "avif"])

    def test_native_diagnostics_do_not_hide_structured_summaries(self):
        results, events = valid_records()
        report = SMOKE.verify_output(serialize(results), "native diagnostic\n" + serialize(events))
        self.assertEqual(report["batch_max"], 4)

    def test_missing_or_duplicate_summary_is_a_failure(self):
        for delta in (-1, 1):
            with self.subTest(delta=delta):
                results, events = valid_records()
                events = events[:-1] if delta < 0 else events + [events[-1]]
                with self.assertRaises(SMOKE.VerificationError):
                    SMOKE.verify_output(serialize(results), serialize(events))

    def test_partial_wrong_or_empty_results_are_rejected(self):
        cases = (("width", 1), ("height", 1), ("text", ""), ("lines", []))
        for key, value in cases:
            with self.subTest(key=key):
                results, events = valid_records()
                results[0][key] = value
                with self.assertRaises(SMOKE.VerificationError):
                    SMOKE.verify_output(serialize(results), serialize(events))

    def test_inactive_optimizations_and_failed_native_stages_are_rejected(self):
        cases = (("success", False), ("failed_stage", "decode"), ("image_format", "jpeg"),
                 ("recognition_provider", "coreml"), ("tensor_backend", "unknown"),
                 ("detection_inference_ms", 0), ("recognition_inference_sum_ms", 0),
                 ("ctc_sum_ms", 0), ("recognition_batch_max", 1),
                 ("recognition_batches", 12), ("buffer_reused", 0))
        for key, value in cases:
            with self.subTest(key=key):
                results, events = valid_records()
                events[4][key] = value  # The second real request must reuse slots.
                with self.assertRaises(SMOKE.VerificationError):
                    SMOKE.verify_output(serialize(results), serialize(events))

    def test_jpeg_and_webp_must_use_native_decode(self):
        for format_name in ("jpeg", "webp"):
            with self.subTest(format_name=format_name):
                results, events = valid_records()
                for event in events:
                    if event.get("image_format") == format_name:
                        event["decode_backend"] = "go"
                with self.assertRaises(SMOKE.VerificationError):
                    SMOKE.verify_output(serialize(results), serialize(events))

    def test_disabled_warmup_or_coreml_in_cpu_image_is_rejected(self):
        for index, key, value in ((2, "warmup_runs", 0), (0, "coreml_cache", True), (0, "stage", "recognition")):
            with self.subTest(key=key):
                results, events = valid_records()
                events[index][key] = value
                with self.assertRaises(SMOKE.VerificationError):
                    SMOKE.verify_output(serialize(results), serialize(events))

    def test_model_preparation_failure_cleans_temporary_directory(self):
        directories = []

        def fail_preparation(command, **_kwargs):
            directories.append(Path(command[-1]).parent)
            raise subprocess.CalledProcessError(1, command)

        with mock.patch.object(SMOKE.subprocess, "run", side_effect=fail_preparation):
            with self.assertRaises(subprocess.CalledProcessError):
                SMOKE.run("inferkit:test", None)
        self.assertEqual(len(directories), 1)
        self.assertFalse(directories[0].exists())

    def test_invalid_container_output_cleans_config_and_skips_download(self):
        with tempfile.TemporaryDirectory(prefix="inferkit-image-fixture-") as directory:
            models = Path(directory)
            for filename in ("det.onnx", "rec.onnx", "dict.txt"):
                (models / filename).write_bytes(b"unit fixture")
            failed = subprocess.CompletedProcess([], 0, "", "")
            with mock.patch.object(SMOKE.subprocess, "run", return_value=failed) as runner:
                with mock.patch.object(SMOKE.sys, "stderr", io.StringIO()):
                    with self.assertRaises(SMOKE.VerificationError):
                        SMOKE.run("inferkit:test", models)
            self.assertEqual(runner.call_count, 2)
            command = runner.call_args_list[0].args[0]
            self.assertEqual(command[:2], ["docker", "run"])
            self.assertIn("--network=none", command)
            self.assertIn("--read-only", command)
            mount = next(value for value in command if "target=/etc/inferkit/ocr-test.toml" in value)
            config = Path(mount.split("source=", 1)[1].split(",target=", 1)[0])
            self.assertFalse(config.parent.exists())
            self.assertTrue((models / "det.onnx").exists())

    def test_timeout_stops_only_the_owned_container(self):
        timeout = subprocess.TimeoutExpired("docker", 180)
        removed = subprocess.CompletedProcess([], 0, "", "")
        with mock.patch.object(SMOKE.subprocess, "run", side_effect=[timeout, removed]) as runner:
            with self.assertRaises(subprocess.TimeoutExpired):
                SMOKE.run_container("inferkit:test", Path("/models"), Path("/config"))
        command = runner.call_args_list[0].args[0]
        name = command[command.index("--name") + 1]
        self.assertTrue(name.startswith("inferkit-check-"))
        self.assertEqual(runner.call_args_list[1].args[0], ["docker", "rm", "--force", name])

    def test_auto_removed_container_does_not_fail_cleanup(self):
        complete = subprocess.CompletedProcess([], 0, "results", "logs")
        absent = subprocess.CompletedProcess([], 1, "", "Error response from daemon: No such container: test")
        with mock.patch.object(SMOKE.subprocess, "run", side_effect=[complete, absent]):
            result = SMOKE.run_container("inferkit:test", Path("/models"), Path("/config"))
        self.assertIs(result, complete)


if __name__ == "__main__":
    unittest.main()
