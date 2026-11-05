#!/usr/bin/env bash
set -euo pipefail

# Prepare the tested official PP-OCRv6 medium pair without Python or Paddle.
fail() { printf '%s\n' "$*" >&2; exit 1; }
[[ $# -ge 1 && $# -le 2 ]] || fail "Usage: $0 OUTPUT_DIRECTORY [OFFLINE_DIRECTORY]"
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
  fetch "$lock/det.tar" "$base/PP-OCRv6_medium_det_onnx_infer.tar"
  fetch "$lock/rec.tar" "$base/PP-OCRv6_medium_rec_onnx_infer.tar"
  fetch "$lock/dict.txt" "https://raw.githubusercontent.com/PaddlePaddle/PaddleOCR/$revision/ppocr/utils/dict/ppocrv6_dict.txt"
fi
verify "$lock/det.tar" c5adb0b15de1b1838934eba1dd72e7529e7d80132216c6ee6d26eba6fa054fcf
verify "$lock/rec.tar" d8cc46c7163c83a151aef8fce5856b965860df90a875029216d605b0f607eaec
verify "$lock/dict.txt" b5f2bfe2bdd9448429e3e82b51c789775d9b42f2403d082b00662eb77e401c5d
mkdir -- "$lock/model"
# Extract only the named weights; archive paths never choose output locations.
tar -xOf "$lock/det.tar" PP-OCRv6_medium_det_onnx_infer/inference.onnx > "$lock/model/det.onnx"
tar -xOf "$lock/rec.tar" PP-OCRv6_medium_rec_onnx_infer/inference.onnx > "$lock/model/rec.onnx"
cp -- "$lock/dict.txt" "$lock/model/dict.txt"
verify "$lock/model/det.onnx" eb13b44b25bb36f89528b68720af8a61d9cf381176107f465db1757b65d086e1
verify "$lock/model/rec.onnx" 9c09abf0957f7968c7586464b7397b84ad2387a0497a351af40e9acc71b673ba
cat > "$lock/model/SHA256SUMS" <<'SUMS'
eb13b44b25bb36f89528b68720af8a61d9cf381176107f465db1757b65d086e1  det.onnx
9c09abf0957f7968c7586464b7397b84ad2387a0497a351af40e9acc71b673ba  rec.onnx
b5f2bfe2bdd9448429e3e82b51c789775d9b42f2403d082b00662eb77e401c5d  dict.txt
SUMS
[[ ! -e "$destination" && ! -L "$destination" ]] || fail "Output appeared during preparation: $destination"
mv -- "$lock/model" "$destination"
printf 'PP-OCRv6 medium prepared: %s\n' "$destination"
