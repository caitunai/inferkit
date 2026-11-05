# Native pipeline reference

`sample10-normalized.png` is the 320×320 RGB output of the training
`normalize_images.py:normalize_pixels` function applied to the existing synthetic
`services/imagefilter/testdata/sample10.avif` (10-bit YUV444 with alpha).

Generated with pyvips 3.2.0 and libvips 8.18.7 on macOS arm64. The reference applies
thumbnail_buffer (size=both, crop=none, auto orientation, nonlinear light), sRGB,
uchar, white flatten with max_alpha=255, another uchar cast and centered white
padding. It specifically detects incorrect right-aligned 10-bit sample handling.
Tests allow at most one RGB level of native cross-platform rounding; Go tensor
conversion is tested separately with exact float32 equality.
