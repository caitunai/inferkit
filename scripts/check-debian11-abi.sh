#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

fail() { printf '%s\n' "$*" >&2; exit 1; }
[[ $# -gt 0 ]] || fail "Usage: $0 ELF_FILE [ELF_FILE...]"
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || fail 'Run ABI checks on Linux x86_64'
for executable in readelf ldd sort; do
  command -v "$executable" >/dev/null || fail "Missing command: $executable"
done

check_versions() {
  local file=$1 namespace=$2 maximum=$3 actual newest versions
  # Only requirements are relevant; a library may export newer symbol definitions.
  versions=$(readelf --version-info --wide -- "$file" | awk -v namespace="$namespace" '
    /^Version needs section/ { requirements=1; next }
    /^Version .* section/ { requirements=0 }
    requirements { for (i=1; i<NF; i++) if ($i == "Name:" && index($(i+1), namespace "_") == 1) print $(i+1) }
  ')
  if [[ -z "$versions" ]]; then return; fi
  actual=$(printf '%s\n' "$versions" | sort -Vu | tail -n 1)
  newest=$(printf '%s\n%s\n' "$actual" "${namespace}_$maximum" | sort -V | tail -n 1)
  [[ "$newest" == "${namespace}_$maximum" ]] || fail "$file requires $actual, above Debian 11's ${namespace}_$maximum baseline"
  printf '  %s maximum required: %s\n' "$namespace" "$actual"
}

for file in "$@"; do
  [[ -f "$file" ]] || fail "Missing ELF file: $file"
  header=$(readelf --file-header --wide -- "$file")
  [[ "$header" == *'ELF64'* && "$header" == *'Advanced Micro Devices X86-64'* ]] || fail "Expected a Linux x86_64 ELF64 file: $file"
  printf 'Checking %s\n' "$file"
  check_versions "$file" GLIBC 2.31
  check_versions "$file" GLIBCXX 3.4.28
  check_versions "$file" CXXABI 1.3.12
  dependencies=$(ldd -- "$file" 2>&1) || fail "$dependencies"
  [[ "$dependencies" != *'not found'* ]] || fail "$dependencies"
  printf '%s\n' "$dependencies"
done
