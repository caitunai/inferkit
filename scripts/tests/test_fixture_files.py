"""Fail before a native build if public image fixtures are LFS pointer text."""

from pathlib import Path
import unittest


PROJECT = Path(__file__).resolve().parents[2]


class ImageFixtures(unittest.TestCase):
    def test_checked_out_fixtures_contain_encoded_images(self):
        directories = ("services/imagefilter/testdata", "services/imagepipeline/testdata", "services/ocr/testdata")
        signatures = {".png": b"\x89PNG\r\n\x1a\n", ".jpeg": b"\xff\xd8\xff", ".webp": b"RIFF"}
        for directory in directories:
            images = sorted(path for path in (PROJECT / directory).iterdir() if path.suffix in (*signatures, ".avif"))
            self.assertTrue(images, "missing public image fixtures: " + directory)
            for image in images:
                with self.subTest(image=str(image.relative_to(PROJECT))):
                    with image.open("rb") as source:
                        header = source.read(64)
                    self.assertFalse(header.startswith(b"version https://git-lfs.github.com/"),
                                     "expected an encoded image, received a Git LFS pointer")
                    if image.suffix == ".avif":
                        self.assertEqual(header[4:8], b"ftyp")
                    else:
                        self.assertTrue(header.startswith(signatures[image.suffix]), "invalid encoded image header")
                    if image.suffix == ".webp":
                        self.assertEqual(header[8:12], b"WEBP")


if __name__ == "__main__":
    unittest.main()
