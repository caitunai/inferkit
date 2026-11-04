#!/usr/bin/env bash
set -euo pipefail

fail() { printf '%s\n' "$*" >&2; exit 1; }
[[ $# -eq 1 ]] || fail "Usage: $0 ONNXRUNTIME_DIRECTORY (run from the Go module root)"
header="$1/include/onnxruntime_c_api.h"
[[ -r "$header" ]] || fail "Missing ONNX Runtime C API header: $header"
binding=$(go list -m -f '{{.Dir}}' github.com/yalue/onnxruntime_go)
api_version() { awk '$1 == "#define" && $2 == "ORT_API_VERSION" { print $3; exit }' "$1"; }
requested=$(api_version "$binding/onnxruntime_c_api.h")
provided=$(api_version "$header")
[[ "$requested" =~ ^[0-9]+$ && "$provided" =~ ^[0-9]+$ ]] || fail 'Missing or invalid ONNX Runtime C API version'
[[ "$requested" == "$provided" ]] || fail "ONNX Runtime C API mismatch: Go binding requests $requested, package provides $provided; update the pinned runtime together with the binding"
printf 'ONNX Runtime C API %s matches the Go binding\n' "$provided"
