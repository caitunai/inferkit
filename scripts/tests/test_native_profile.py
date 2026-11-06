"""Validate exclusive CPU attribution and eval-window filtering on exported data."""

import importlib.util
from pathlib import Path
import tempfile
import unittest

PROJECT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("native_profile", PROJECT / "scripts/profile-image-native.py")
PROFILE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROFILE)


class NativeProfileAnalysis(unittest.TestCase):
    def test_nested_decoder_is_not_charged_to_its_resampling_caller(self):
        frames = (("decode_mcu_AC_refine", "libjpeg.62.dylib"),
                  ("vips_reducev_uchar_vector_gen", "libvips.42.dylib"),
                  ("vips_image_write_to_memory", "libvips.42.dylib"))
        self.assertEqual(PROFILE.category(frames), "jpeg_entropy_and_coefficients")

    def test_decoder_colour_and_idct_remain_distinct(self):
        self.assertEqual(PROFILE.category((("jsimd_ycc_extrgb_convert_neon", "libjpeg.62.dylib"),)),
                         "jpeg_colour_and_upsampling")
        self.assertEqual(PROFILE.category((("jsimd_idct_4x4_neon", "libjpeg.62.dylib"),)), "jpeg_idct")

    def test_copy_is_attributed_to_nearest_known_work(self):
        frames = (("_platform_memmove", "libsystem_platform.dylib"),
                  ("vips_reduceh_gen", "libvips.42.dylib"),
                  ("sink_memory_area_work_fn", "libvips.42.dylib"))
        self.assertEqual(PROFILE.category(frames), "vips_resampling_and_regions")
        self.assertEqual(PROFILE.category((("unresolved", "unknown"),)), "other")

    def test_border_padding_inside_resampler_is_not_final_output(self):
        frames = (("vips_embed_base_copy_pixel", "libvips.42.dylib"),
                  ("vips_embed_base_gen", "libvips.42.dylib"),
                  ("vips_reduceh_uchar_vector_gen", "libvips.42.dylib"),
                  ("sink_memory_area_work_fn", "libvips.42.dylib"))
        self.assertEqual(PROFILE.category(frames), "vips_resampling_and_regions")

    def test_canvas_copy_and_generic_runtime_are_not_conflated(self):
        output = (("_platform_memmove", "libsystem_platform.dylib"),
                  ("vips_region_copy", "libvips.42.dylib"),
                  ("sink_memory_area_work_fn", "libvips.42.dylib"))
        runtime = (("g_hash_table_lookup", "libglib-2.0.dylib"),
                   ("vips_tile_cache_gen", "libvips.42.dylib"),
                   ("sink_memory_area_work_fn", "libvips.42.dylib"))
        self.assertEqual(PROFILE.category(output), "canvas_output_pixels")
        self.assertEqual(PROFILE.category(runtime), "vips_pipeline_runtime")

    def test_only_running_samples_inside_eval_are_counted_once(self):
        with tempfile.TemporaryDirectory(prefix="inferkit-profile-test-") as directory:
            root = Path(directory)
            self.write_intervals(root)
            self.write_samples(root)
            report = PROFILE.analyze(root / "samples.xml", root / "intervals.xml")
            self.assertEqual(report["samples"], 1799)
            self.assertEqual(report["nonrunning"], 1)
            self.assertEqual(report["outside_or_trimmed"], 1201)
            self.assertEqual(report["sample_weight_ns"], 1799 * 1_000_000)
            self.assertEqual(report["category_percent"], {"jpeg_entropy_and_coefficients": 100.0})
            self.assertEqual(len(report["top_leaf"]), 1)

    @staticmethod
    def write_intervals(root):
        columns = ("start", "duration", "name", "subsystem", "start-thread", "end-thread")
        schema = "".join(f"<col><mnemonic>{name}</mnemonic></col>" for name in columns)
        row = ('<row><start-time>1600000000</start-time><duration>1800000000</duration>'
               '<signpost-name>native_eval</signpost-name><subsystem>com.inferkit.native-profile</subsystem>'
               '<thread id="1">worker</thread><thread ref="1"/></row>')
        (root / "intervals.xml").write_text(f"<root><schema>{schema}</schema>{row}</root>")

    @staticmethod
    def write_samples(root):
        columns = ("time", "thread-state", "weight", "stack")
        schema = "".join(f"<col><mnemonic>{name}</mnemonic></col>" for name in columns)
        definitions = ('<weight id="1">1000000</weight><thread-state id="2">Running</thread-state>'
                       '<thread-state id="3">Waiting</thread-state><tagged-backtrace id="4">'
                       '<frame id="5" name="decode_mcu_AC_refine"><binary id="6" name="libjpeg.62.dylib"/></frame>'
                       '<frame name="vips_reducev_gen"><binary name="libvips.42.dylib"/></frame>'
                       '</tagged-backtrace>')
        rows = []
        for index in range(3001):
            state = 3 if index == 1000 else 2
            rows.append(f'<row><sample-time>{(index + 1000) * 1_000_000}</sample-time>'
                        f'<thread-state ref="{state}"/><weight ref="1"/><tagged-backtrace ref="4"/></row>')
        (root / "samples.xml").write_text(f"<root><schema>{schema}</schema>{definitions}{''.join(rows)}</root>")


if __name__ == "__main__":
    unittest.main()
