#!/usr/bin/env bash
set -euo pipefail

# Match onnxruntime_go's C API 31. Verify the official release before extraction.
version=1.31.1
package="onnxruntime-linux-x64-$version"
digest=c849e36a1ef3e22d4ff217b9d1ce4a3cf5b9467b975610913ae959ad5b16bdc9

fail() { printf '%s\n' "$*" >&2; exit 1; }
[[ $# -ge 1 && $# -le 2 ]] || fail "Usage: $0 OUTPUT_DIRECTORY [OFFLINE_ARCHIVE]"
destination=${1%/}
[[ -n "$destination" && ! -e "$destination" && ! -L "$destination" ]] || fail "Output already exists or is invalid: $1"
for executable in tar sha256sum; do
  command -v "$executable" >/dev/null || fail "Missing command: $executable"
done
mkdir -p -- "$(dirname -- "$destination")"
destination="$(cd -- "$(dirname -- "$destination")" && pwd)/$(basename -- "$destination")"
lock="$destination.prepare-lock"
mkdir -- "$lock" || fail "Another preparation is running, or a stale lock exists: $lock"
trap 'rm -rf -- "$lock"' EXIT

archive="$lock/archive.tgz"
if [[ $# -eq 2 ]]; then
  cp -- "$2" "$archive"
else
  command -v curl >/dev/null || fail 'Missing command: curl'
  curl --fail --location --proto '=https' --tlsv1.2 --connect-timeout 10 \
    --max-time 180 --retry 2 --retry-max-time 300 \
    --output "$archive" "https://github.com/microsoft/onnxruntime/releases/download/v$version/$package.tgz"
fi
checksum=$(sha256sum < "$archive")
[[ "${checksum%% *}" == "$digest" ]] || fail 'ONNX Runtime archive SHA-256 mismatch'
tar -xzf "$archive" -C "$lock"
library="$lock/$package/lib/libonnxruntime.so.$version"
[[ -f "$library" && "$(cat "$lock/$package/VERSION_NUMBER")" == "$version" ]] || fail 'Invalid ONNX Runtime package layout'
[[ ! -e "$destination" && ! -L "$destination" ]] || fail "Output appeared during preparation: $destination"
mv -- "$lock/$package" "$destination"
printf 'ONNX Runtime CPU %s prepared: %s/lib/libonnxruntime.so.%s\n' "$version" "$destination" "$version"
