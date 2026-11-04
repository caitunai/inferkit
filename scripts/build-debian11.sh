#!/usr/bin/env bash
set -euo pipefail

fail() { printf '%s\n' "$*" >&2; exit 1; }
[[ $# -le 1 ]] || fail "Usage: $0 [OUTPUT_DIRECTORY]"
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || fail 'Build on Debian 11 x86_64; macOS cross-compilation is not supported by this script'
# os-release belongs to the build host, not to untrusted package input.
source /etc/os-release
[[ "${ID:-}" == debian && "${VERSION_ID:-}" == 11 ]] || fail 'Use a Debian 11 build host to preserve the glibc 2.31 baseline'
for executable in go cc node npm readelf ldd sha256sum tar dpkg; do
  command -v "$executable" >/dev/null || fail "Missing command: $executable"
done
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
output=${1:-"$project/dist/debian11-amd64"}
output=${output%/}
[[ -n "$output" && ! -e "$output" && ! -L "$output" ]] || fail "Output already exists or is invalid: $output"
mkdir -p -- "$(dirname -- "$output")"
output="$(cd -- "$(dirname -- "$output")" && pwd)/$(basename -- "$output")"
cd -- "$project"

required=$(awk '$1 == "go" { print $2; exit }' go.mod)
installed=$(go env GOVERSION)
dpkg --compare-versions "${installed#go}" ge "$required" || fail "Go $required or newer is required"
node -e 'const [a,b]=process.versions.node.split(".").map(Number); if (!((a===20 && b>=19) || a>22 || (a===22 && b>=12))) process.exit(1)' \
  || fail 'Vite requires Node 20.19+ (20.x) or Node 22.12+'
stage=$(mktemp -d "$(dirname -- "$output")/.inferkit-build.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT

if [[ -n "${ONNXRUNTIME_ARCHIVE:-}" ]]; then
  bash scripts/prepare-onnxruntime-linux.sh "$stage/onnxruntime" "$ONNXRUNTIME_ARCHIVE"
else
  bash scripts/prepare-onnxruntime-linux.sh "$stage/onnxruntime"
fi
go mod download
bash scripts/check-onnxruntime-api.sh "$stage/onnxruntime"

(cd storage/ui && npm ci && npm run build)
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GOAMD64=v1 go build -trimpath -o "$stage/inferkit" .
version=$(cat "$stage/onnxruntime/VERSION_NUMBER")
bash scripts/check-debian11-abi.sh "$stage/inferkit" "$stage/onnxruntime/lib/libonnxruntime.so.$version" "$stage/onnxruntime/lib/libonnxruntime_providers_shared.so"
cp scripts/check-debian11-abi.sh "$stage/check-abi.sh"
cp deploy/debian11/inferkit.service "$stage/inferkit.service"
go version -m "$stage/inferkit" > "$stage/build-info.txt"
mv -T -- "$stage" "$output"
printf 'Debian 11 CPU bundle prepared: %s\n' "$output"
