#!/usr/bin/env python3
"""Compare isolated native pipelines without changing application configuration."""

import argparse
import csv
import json
import os
from pathlib import Path
import shlex
import statistics
import subprocess
import sys
import tempfile

PROJECT = Path(__file__).resolve().parents[1]
VARIANTS = (("A", "A", 1), ("B", "B", 1), ("C", "C", 1), ("D", "D", 1),
            ("E", "E", 1), ("F2", "A", 2), ("F4", "A", 4), ("F8", "A", 8))


def command(arguments):
    result = subprocess.run(arguments, capture_output=True, text=True, check=True)
    return result.stdout


def compile_benchmark(directory):
    flags = shlex.split(command(["pkg-config", "--cflags", "--libs", "vips", "libheif"]))
    binary = Path(directory) / "native-trial"
    arguments = [*shlex.split(os.environ.get("CC", "cc")), "-O2", "-Wall", "-Wextra",
                 str(PROJECT / "scripts/bench-image-native.c"), "-o", str(binary), *flags]
    if sys.platform.startswith("linux"):
        arguments.append("-ldl")
    command(arguments)
    return binary


def source_cases(arguments):
    cases = [{"source": str(path.resolve()), "name": path.name, "quality_only": False}
             for path in arguments.images]
    cases.extend({"source": str(path.resolve()), "name": path.name, "quality_only": True}
                 for path in arguments.quality_sources)
    if arguments.fixtures:
        root = arguments.fixtures.resolve()
        report = json.loads((root / "validation.json").read_text())
        for sample in report["samples"]:
            source = (root / sample["source_file"]).resolve()
            if not source.is_relative_to(root):
                raise ValueError("fixture source must stay within the model directory")
            cases.append({"source": str(source), "name": "fixture/" + source.name, "quality_only": True})
    for index, case in enumerate(cases):
        if not Path(case["source"]).is_file():
            raise ValueError("missing input: " + case["source"])
        case["id"] = f"case{index:03d}"
    return cases


def measure(binary, output, case, variant, callers, samples):
    tag, operation, threads = variant
    rgb = f"pixels/{case['id']}_{tag}.rgb"
    result = json.loads(command([str(binary), operation, str(threads), case["source"],
                                 str(samples), str(callers), str(output / rgb)]))
    result.update({"case": case["id"], "name": case["name"], "variant": tag,
                   "threads": threads, "callers": callers, "rgb": rgb})
    return result


def pixel_difference(reference, actual):
    if len(reference) != 320 * 320 * 3 or len(actual) != len(reference):
        raise ValueError("invalid native RGB canvas")
    differences = [abs(left - right) for left, right in zip(reference, actual)]
    return {"max_pixel_error": max(differences), "changed_components": sum(value != 0 for value in differences),
            "rms_pixel_error": (sum(value * value for value in differences) / len(differences)) ** 0.5}


def summarize(rows):
    groups = {}
    for row in rows:
        groups.setdefault((row["case"], row["variant"], row["callers"]), []).append(row)
    results = []
    for group in groups.values():
        result = {key: value for key, value in group[0].items() if not isinstance(value, (int, float))}
        for key in ("threads", "callers", "samples", "jpeg_shrink", "jpeg_width", "jpeg_height"):
            result[key] = group[0][key]
        for key in ("p50_ms", "p95_ms", "p99_ms", "images_per_second", "cpu_percent", "peak_rss_bytes",
                    "header_ms", "prepare_ms", "eval_ms", "cleanup_ms"):
            result[key] = statistics.median(row[key] for row in group)
        result["rounds"] = len(group)
        result["jpeg_multiscan"] = group[0]["jpeg_multiscan"]
        results.append(result)
    return results


def execute(arguments, cases, output, binary):
    rows = []
    for trial in range(arguments.rounds):
        ordered = list(VARIANTS)
        # Rotate order to avoid consistently giving any variant a cold/late slot.
        ordered = ordered[trial % len(ordered):] + ordered[:trial % len(ordered)]
        if trial % 2:
            ordered.reverse()
        for case in cases:
            if case["quality_only"]:
                continue
            for callers in arguments.callers:
                for variant in ordered:
                    row = measure(binary, output, case, variant, callers, arguments.samples)
                    row["round"] = trial
                    rows.append(row)
                    with (output / "raw.jsonl").open("a") as stream:
                        stream.write(json.dumps(row) + "\n")
                    print(f"round={trial + 1} {case['name']} {variant[0]} callers={callers} "
                          f"P50={row['p50_ms']:.3f} P95={row['p95_ms']:.3f} "
                          f"P99={row['p99_ms']:.3f} throughput={row['images_per_second']:.1f}", flush=True)
    pixels = []
    for case in cases:
        if case["quality_only"]:
            for variant in VARIANTS:
                measure(binary, output, case, variant, 1, 1)
        reference = (output / f"pixels/{case['id']}_A.rgb").read_bytes()
        for tag, _, threads in VARIANTS:
            rgb = f"pixels/{case['id']}_{tag}.rgb"
            pixels.append({"case": case["id"], "name": case["name"], "variant": tag,
                           "threads": threads, "rgb": rgb,
                           **pixel_difference(reference, (output / rgb).read_bytes())})
    summary = summarize(rows)
    report = {"vips_version": command(["pkg-config", "--modversion", "vips"]).strip(),
              "scope": "native image lifecycle only; excludes Go, HTTP and ONNX",
              "warmup_per_process": 5, "percentiles": "median of round percentiles; stages are means",
              "cases": cases, "pixels": pixels, "summary": summary}
    (output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    with (output / "summary.csv").open("w", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(summary[0]))
        writer.writeheader()
        writer.writerows(summary)
    print("Report: " + str(output / "report.json"), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--images", nargs="+", type=Path, required=True)
    parser.add_argument("--quality-sources", nargs="*", type=Path, default=[])
    parser.add_argument("--fixtures", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--samples", type=int, default=100)
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--callers", nargs="+", type=int, choices=(1, 8), default=[1, 8])
    arguments = parser.parse_args()
    if not 1 <= arguments.samples <= 10000 or not 1 <= arguments.rounds <= 20:
        parser.error("samples must be 1..10000 and rounds 1..20")
    output = arguments.output.resolve()
    if output.exists():
        parser.error("output must be a new directory")
    cases = source_cases(arguments)
    output.mkdir(parents=True)
    (output / "pixels").mkdir()
    with tempfile.TemporaryDirectory(prefix="inferkit-native-trial-") as directory:
        binary = compile_benchmark(directory)
        execute(arguments, cases, output, binary)


if __name__ == "__main__":
    main()
