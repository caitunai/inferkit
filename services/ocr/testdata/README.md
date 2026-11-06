# Synthetic OCR release fixture

`ci.png` is a locally generated 320×640 grayscale PNG containing twelve lines,
`OCR 01` through `OCR 12`. It contains no user data or third-party document.
It exercises real detection, CTC recognition, same-width batching and repeated
input buffer reuse in the Debian 13 image release check.

Generated with libvips 8.18.7: `vips text` using `Arial 32`, line spacing 10;
invert the mask with `vips invert`, then embed at (24,24) in a white 320×640
canvas. The checked-in pixels are the fixture; build runners need no font or
text-rendering dependency. Four-format decoding uses the existing synthetic
48×32 images under `services/imagefilter/testdata`.
