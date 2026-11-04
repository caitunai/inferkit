# Image-format fixtures

These small images were generated for this project; they contain no external artwork.
The source is a 48x32 RGB gradient with an alpha gradient in the upper-right quadrant.

- `sample.png`: original straight-alpha PNG.
- `sample.jpeg`: JPEG quality 95 after compositing onto white.
- `sample.webp`: lossless WebP with exact transparent RGB preservation.
- `sample.avif`: lossless 8-bit AVIF, full-range identity matrix, with alpha.
- `sample10.avif`: 10-bit YUV444 AVIF, with alpha (color conversion is not lossless).
- `rotated.avif`: same pixels as `sample.avif`, with container irot=1 (90 degrees counterclockwise).
- `rotated.png`: independent physical 90-degree counterclockwise rotation of `sample.png`.

The Go tests read these committed files; external encoders are not needed to run tests or deploy.
The AVIF files were produced with libavif's avifenc, WebP with cwebp:

```sh
cwebp -lossless -exact sample.png -o sample.webp
avifenc --lossless --speed 10 --jobs 1 sample.png sample.avif
avifenc --lossless --speed 10 --jobs 1 --irot 1 sample.png rotated.avif
avifenc -q 100 --depth 10 --yuv 444 --speed 10 --jobs 1 sample.png sample10.avif
```
