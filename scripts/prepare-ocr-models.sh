#!/usr/bin/env bash
set -euo pipefail

# Prepare pinned official PP-OCRv6 medium/small pairs without Python or Paddle.
fail() { printf '%s\n' "$*" >&2; exit 1; }
size=medium
if [[ ${1:-} == --size ]]; then
  [[ $# -ge 3 ]] || fail "Usage: $0 [--size medium|small] OUTPUT_DIRECTORY [OFFLINE_DIRECTORY]"
  size=$2
  shift 2
fi
[[ $# -ge 1 && $# -le 2 ]] || fail "Usage: $0 [--size medium|small] OUTPUT_DIRECTORY [OFFLINE_DIRECTORY]"
case "$size" in
  medium)
    det_archive=c5adb0b15de1b1838934eba1dd72e7529e7d80132216c6ee6d26eba6fa054fcf
    rec_archive=d8cc46c7163c83a151aef8fce5856b965860df90a875029216d605b0f607eaec
    det_model=eb13b44b25bb36f89528b68720af8a61d9cf381176107f465db1757b65d086e1
    rec_model=9c09abf0957f7968c7586464b7397b84ad2387a0497a351af40e9acc71b673ba
    ;;
  small)
    det_archive=d218f6fbf0f1c23d2161bd6ac7f5eaa6104fa89955c09290497e31008e2618e4
    rec_archive=d267ab077a44a0eedb1ea8f8c542d263f211de8e9d7a029bf9fcfff7e5a88fb1
    det_model=d73e0058b7a8086bbd57f3d10b8bcd4ff95363f67e06e2762b5e814fe9c9410e
    rec_model=5435fd747c9e0efe15a96d0b378d5bd157e9492ed8fd80edf08f30d02fa24634
    ;;
  *) fail "Unsupported model size: $size" ;;
esac
destination=${1%/}
[[ -n "$destination" && ! -e "$destination" && ! -L "$destination" ]] || fail "Output already exists or is invalid: $1"
command -v tar >/dev/null || fail 'Missing command: tar'
if command -v sha256sum >/dev/null; then
  checksum() { sha256sum < "$1"; }
else
  command -v shasum >/dev/null || fail 'Missing SHA-256 tool: sha256sum or shasum'
  checksum() { shasum -a 256 < "$1"; }
fi
verify() {
  local actual
  actual=$(checksum "$1")
  [[ "${actual%% *}" == "$2" ]] || fail "SHA-256 mismatch: $(basename -- "$1")"
}
mkdir -p -- "$(dirname -- "$destination")"
destination="$(cd -- "$(dirname -- "$destination")" && pwd)/$(basename -- "$destination")"
lock="$destination.prepare-lock"
mkdir -- "$lock" || fail "Preparation lock already exists: $lock"
trap 'rm -rf -- "$lock"' EXIT

base=https://paddle-model-ecology.bj.bcebos.com/paddlex/official_inference_model/paddle3.0.0
revision=b03f46425e8ff4442b268ce449e3eef758146cd4
fetch() {
  curl --fail --location --proto '=https' --tlsv1.2 --connect-timeout 15 \
    --max-time 300 --retry 2 --retry-max-time 600 --output "$1" "$2"
}
if [[ $# -eq 2 ]]; then
  cp -- "$2/det.tar" "$lock/det.tar"
  cp -- "$2/rec.tar" "$lock/rec.tar"
  cp -- "$2/dict.txt" "$lock/dict.txt"
else
  command -v curl >/dev/null || fail 'Missing command: curl'
  fetch "$lock/det.tar" "$base/PP-OCRv6_${size}_det_onnx_infer.tar"
  fetch "$lock/rec.tar" "$base/PP-OCRv6_${size}_rec_onnx_infer.tar"
  fetch "$lock/dict.txt" "https://raw.githubusercontent.com/PaddlePaddle/PaddleOCR/$revision/ppocr/utils/dict/ppocrv6_dict.txt"
fi
verify "$lock/det.tar" "$det_archive"
verify "$lock/rec.tar" "$rec_archive"
verify "$lock/dict.txt" b5f2bfe2bdd9448429e3e82b51c789775d9b42f2403d082b00662eb77e401c5d
mkdir -- "$lock/model"
# Extract only the named weights; archive paths never choose output locations.
tar -xOf "$lock/det.tar" "PP-OCRv6_${size}_det_onnx_infer/inference.onnx" > "$lock/model/det.onnx"
tar -xOf "$lock/rec.tar" "PP-OCRv6_${size}_rec_onnx_infer/inference.onnx" > "$lock/model/rec.onnx"
cp -- "$lock/dict.txt" "$lock/model/dict.txt"
verify "$lock/model/det.onnx" "$det_model"
verify "$lock/model/rec.onnx" "$rec_model"
printf '%s  det.onnx\n%s  rec.onnx\n%s  dict.txt\n' "$det_model" "$rec_model" \
  b5f2bfe2bdd9448429e3e82b51c789775d9b42f2403d082b00662eb77e401c5d > "$lock/model/SHA256SUMS"
[[ ! -e "$destination" && ! -L "$destination" ]] || fail "Output appeared during preparation: $destination"
mv -- "$lock/model" "$destination"
printf 'PP-OCRv6 %s prepared: %s\n' "$size" "$destination"
