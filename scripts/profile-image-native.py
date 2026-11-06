#!/usr/bin/env python3
"""Sample Running CPU stacks within native_eval signposts on macOS only."""

import argparse
from bisect import bisect_right
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import time
from xml.etree import ElementTree as ET

PROJECT = Path(__file__).resolve().parents[1]
RESAMPLE = ("vips_reduceh", "vips_reducev", "vips_shrinkh", "vips_shrinkv", "vips_resize", "vips_affine")
COLOUR = ("colourspace", "icc_transform", "icc_import", "colour_gen", "sRGB2", "RGB2", "XYZ2", "Lab2")
CANVAS = ("embed_base_gen", "embed_gen", "sink_memory_area_work", "vips_image_write_to_memory")
ENTROPY = ("decode_mcu", "huff", "bit_buffer", "consume", "read_mark", "input_pass")
JPEG_COLOUR = ("ycc", "rgb", "color_convert", "upsample", "merged_", "h2v")


def command(arguments):
    return subprocess.run(arguments, check=True, capture_output=True, text=True).stdout


class Table:
    """Resolve xctrace's typed id/ref values within a single exported table."""

    def __init__(self, path):
        self.root = ET.parse(path).getroot()
        self.references = {node.get("id"): node for node in self.root.iter() if node.get("id")}
        schema = self.root.find(".//schema")
        if schema is None:
            raise ValueError("missing xctrace schema")
        self.columns = [node.text for node in schema.findall("col/mnemonic")]

    def resolve(self, node):
        while node.get("ref"):
            node = self.references[node.get("ref")]
        return node

    def rows(self):
        for row in self.root.findall(".//row"):
            yield dict(zip(self.columns, (self.resolve(node) for node in row)))

    def frames(self, stack):
        frames = []
        for node in stack:
            if node.tag != "frame":
                continue
            frame = self.resolve(node)
            binary = frame.find("binary")
            binary = self.resolve(binary).get("name", "unknown") if binary is not None else "unknown"
            frames.append((frame.get("name", "unknown"), binary))
        return tuple(frames)


def category(frames):
    # Frames are PC/leaf first. The first specialized caller owns generic helper
    # work too; outer reduce callbacks must never count nested JPEG decode again.
    canvas = False
    for name, binary in frames:
        if "libjpeg" in binary:
            lowered = name.lower()
            if any(token in lowered for token in ENTROPY):
                return "jpeg_entropy_and_coefficients"
            if "idct" in lowered:
                return "jpeg_idct"
            if any(token in lowered for token in JPEG_COLOUR):
                return "jpeg_colour_and_upsampling"
            return "jpeg_other"
        if any(token in name for token in RESAMPLE):
            return "vips_resampling_and_regions"
        if any(token in name for token in COLOUR):
            return "vips_colour"
        if any(token in name for token in CANVAS):
            # Embed also supplies border pixels *inside* a resampler. Continue
            # searching so those samples belong to resampling, not final pad.
            canvas = True
    if canvas and frames:
        name = frames[0][0]
        scratch = any(any(token in frame[0] for token in ("vips_tile_cache", "vips_buffer", "vips_tracked_aligned_alloc")) for frame in frames)
        direct = ("vips_region_copy", "vips_region_paint", "vips_embed_base", "vips_image_write_to_memory")
        copying = ("_platform_memmove", "_platform_memcpy", "_platform_memset")
        if any(token in name for token in direct) or name in copying and not scratch:
            return "canvas_output_pixels"
    if any("libvips" in binary for _, binary in frames):
        return "vips_pipeline_runtime"
    return "other"


def eval_spans(table):
    spans = []
    for row in table.rows():
        if row["name"].text != "native_eval" or row["subsystem"].text != "com.inferkit.native-profile":
            continue
        if row["start-thread"].tag == "sentinel" or row["end-thread"].tag == "sentinel":
            continue
        if not row["start"].text or not row["duration"].text:
            continue
        start, duration = int(row["start"].text), int(row["duration"].text)
        if duration > 0:
            spans.append((start, start + duration))
    spans.sort()
    if not spans or any(left[1] > right[0] for left, right in zip(spans, spans[1:])):
        raise ValueError("missing or overlapping single-caller native_eval intervals")
    return spans


def analyze(samples_path, intervals_path):
    spans = eval_spans(Table(intervals_path))
    starts = [start for start, _ in spans]
    table = Table(samples_path)
    rows = list(table.rows())
    if not rows:
        raise ValueError("no CPU samples")
    lower = min(int(row["time"].text) for row in rows) + 500_000_000
    upper = max(int(row["time"].text) for row in rows) - 500_000_000
    categories, leaves, stacks = Counter(), Counter(), Counter()
    cache = {}
    included = outside = nonrunning = 0
    for row in rows:
        timestamp = int(row["time"].text)
        index = bisect_right(starts, timestamp) - 1
        if timestamp < lower or timestamp > upper or index < 0 or timestamp >= spans[index][1]:
            outside += 1
            continue
        if row["thread-state"].text != "Running":
            nonrunning += 1
            continue
        stack = row["stack"]
        identity = stack.get("id")
        if identity not in cache:
            cache[identity] = table.frames(stack)
        frames = cache[identity]
        weight = int(row["weight"].text)
        categories[category(frames)] += weight
        leaves[frames[0] if frames else ("unknown", "unknown")] += weight
        stacks[frames] += weight
        included += 1
    total = sum(categories.values())
    if included < 1000 or total <= 0:
        raise ValueError("insufficient Running eval samples")
    return {"samples": included, "outside_or_trimmed": outside, "nonrunning": nonrunning,
            "eval_intervals": len(spans), "sample_weight_ns": total,
            "category_weight_ns": dict(categories),
            "category_percent": {name: 100 * value / total for name, value in categories.items()},
            "top_leaf": [{"function": frame[0], "binary": frame[1], "weight_ns": value,
                          "percent": 100 * value / total} for frame, value in leaves.most_common(30)],
            "top_stacks": [{"frames": frames, "weight_ns": value, "percent": 100 * value / total}
                           for frames, value in stacks.most_common(10)],
            "scope": "Running CPU sample weights within native_eval; excludes first/last 0.5s; not wall-time percentages"}


def export(trace, schema, output):
    query = f'/trace-toc/run[@number="1"]/data/table[@schema="{schema}"]'
    command(["xcrun", "xctrace", "export", "--input", str(trace), "--xpath", query, "--output", str(output)])


def capture(binary, arguments, folder):
    rgb = folder / "pixels.rgb"
    with (folder / "target.log").open("w") as stream:
        process = subprocess.Popen([str(binary), arguments.variant, "1", str(arguments.image),
                                    "10000", "1", str(rgb)], stdout=stream, stderr=stream)
        try:
            deadline = time.monotonic() + 15
            while not rgb.exists():
                if process.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError("profile target failed to finish warmup")
                time.sleep(0.02)
            trace = folder / "recording.trace"
            record = command(["xcrun", "xctrace", "record", "--template", "Time Profiler",
                              "--output", str(trace), "--time-limit", f"{arguments.seconds}s",
                              "--no-prompt", "--attach", str(process.pid)])
            (folder / "recorder.log").write_text(record)
        finally:
            if process.poll() is None:
                process.terminate()
            process.wait()
    export(trace, "time-profile", folder / "samples.xml")
    export(trace, "OSSignpostIntervals", folder / "intervals.xml")
    report = analyze(folder / "samples.xml", folder / "intervals.xml")
    (folder / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    return report


def build(directory, name):
    binary = Path(directory) / name
    flags = shlex.split(command(["pkg-config", "--cflags", "--libs", "vips", "libheif"]))
    source = "profile-image-native.c" if name == "profile" else "bench-image-native.c"
    command([*shlex.split(os.environ.get("CC", "cc")), "-O2", "-g", "-fno-omit-frame-pointer",
             "-Wall", "-Wextra", str(PROJECT / "scripts" / source), "-o", str(binary), *flags])
    return binary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--seconds", type=int, default=15)
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--variant", choices=("A", "B", "D"), default="A")
    arguments = parser.parse_args()
    if sys.platform != "darwin":
        parser.error("capture requires macOS and Xcode Time Profiler")
    if not 5 <= arguments.seconds <= 30 or not 1 <= arguments.rounds <= 10:
        parser.error("seconds must be 5..30 and rounds 1..10")
    arguments.image = arguments.image.resolve()
    arguments.output = arguments.output.resolve()
    if not arguments.image.is_file() or arguments.output.exists():
        parser.error("image must exist and output must be a new directory")
    arguments.output.mkdir(parents=True)
    reports = []
    with tempfile.TemporaryDirectory(prefix="inferkit-native-profile-") as directory:
        binary, reference = build(directory, "profile"), build(directory, "benchmark")
        reference_rgb = arguments.output / "reference.rgb"
        reference_metrics = json.loads(command([str(reference), arguments.variant, "1", str(arguments.image), "1", "1", str(reference_rgb)]))
        for trial in range(arguments.rounds):
            folder = arguments.output / f"round{trial + 1}"
            folder.mkdir()
            report = capture(binary, arguments, folder)
            if reference_rgb.read_bytes() != (folder / "pixels.rgb").read_bytes():
                raise ValueError("sampling wrapper changed native pixels")
            reports.append(report)
            print(f"round={trial + 1} samples={report['samples']} {report['category_percent']}", flush=True)
    total = Counter()
    for report in reports:
        total.update(report["category_weight_ns"])
    weight = sum(total.values())
    summary = {"image": str(arguments.image), "image_sha256": hashlib.sha256(arguments.image.read_bytes()).hexdigest(),
               "variant": arguments.variant, "rounds": len(reports), "threads": 1,
               "samples": sum(report["samples"] for report in reports),
               "category_percent": {name: 100 * value / weight for name, value in total.items()},
               "scope": reports[0]["scope"], "pixel_exact_with_unprofiled": True,
               "jpeg": {key: reference_metrics[key] for key in ("jpeg_shrink", "jpeg_multiscan", "jpeg_width", "jpeg_height")},
               "vips_version": command(["pkg-config", "--modversion", "vips"]).strip(),
               "source_sha256": {name: hashlib.sha256((PROJECT / name).read_bytes()).hexdigest()
                                 for name in ("scripts/profile-image-native.c", "scripts/bench-image-native.c", "services/internal/vips/native_vips.c")}}
    (arguments.output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")


if __name__ == "__main__":
    main()
