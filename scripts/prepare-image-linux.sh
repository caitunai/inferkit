#!/usr/bin/env bash
# Build private native libraries, never replace /usr/lib or Homebrew libraries.
set -euo pipefail
fail() { printf '%s\n' "$*" >&2; exit 1; }
[[ $# -ge 1 && $# -le 2 ]] || fail "Usage: $0 OUTPUT_DIRECTORY [VERIFIED_SOURCE_ARCHIVES_DIRECTORY]"
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || fail 'Build native image libraries on Linux x86_64 for the Debian 13 image'
for executable in cc c++ cmake meson ninja nasm pkg-config curl sha256sum tar dpkg-query getconf; do
  command -v "$executable" >/dev/null || fail "Missing command: $executable"
done
[[ "$(dpkg-query -W -f='${db:Status-Abbrev}' libjpeg62-turbo-dev 2>/dev/null)" == ii* ]] \
  || fail 'Install libjpeg62-turbo-dev; Linux JPEG decoding must use libjpeg-turbo'
jpeg_pc=$(dpkg-query -L libjpeg62-turbo-dev | awk '/\/libjpeg[.]pc$/ { print; exit }')
[[ -n "$jpeg_pc" ]] || fail 'libjpeg62-turbo-dev did not supply libjpeg.pc'
output=${1%/}
[[ -n "$output" && ! -e "$output" && ! -L "$output" ]] || fail "Output already exists or is invalid: $output"
mkdir -p -- "$(dirname -- "$output")"
output="$(cd -- "$(dirname -- "$output")" && pwd)/$(basename -- "$output")"
archives=${2:-}
[[ -z "$archives" ]] || archives="$(cd -- "$archives" && pwd)"
work=$(mktemp -d "$(dirname -- "$output")/.inferkit-image.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
jobs=${INFERKIT_BUILD_JOBS:-$(getconf _NPROCESSORS_ONLN)}
[[ "$jobs" =~ ^[1-9][0-9]*$ ]] || fail 'INFERKIT_BUILD_JOBS must be a positive integer'
jobs=$((jobs > 8 ? 8 : jobs))
mkdir -- "$output"
chmod 0755 "$output"

fetch() {
  local name=$1 expected=$2 url=$3
  if [[ -n "$archives" ]]; then
    cp -- "$archives/$name" "$work/$name"
  else
    curl --fail --location --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 --retry 2 \
      --output "$work/$name" "$url"
  fi
  [[ "$(sha256sum "$work/$name" | cut -d' ' -f1)" == "$expected" ]] || fail "SHA-256 mismatch: $name"
  tar -xf "$work/$name" -C "$work" --no-same-owner
}

fetch dav1d-1.5.4.tar.xz 686616b7c69eb88d44459391ab25cac13b6647a3b288835c5784e71c1514a5c5 \
  https://download.videolan.org/pub/videolan/dav1d/1.5.4/dav1d-1.5.4.tar.xz
fetch highway-1.4.0.tar.gz e72241ac9524bb653ae52ced768b508045d4438726a303f10181a38f764a453c \
  https://github.com/google/highway/archive/refs/tags/1.4.0.tar.gz
fetch libheif-1.23.7.tar.gz f7483e818503ea6224c8863e4a56ac02c7089021a98472c811c12aa19faf2475 \
  https://github.com/strukturag/libheif/releases/download/v1.23.7/libheif-1.23.7.tar.gz
fetch vips-8.18.7.tar.xz 5baaead3b0bb20ffdb9e9ff09aa9fda08620923df77b63b436654cb5e0b3bf94 \
  https://github.com/libvips/libvips/releases/download/v8.18.7/vips-8.18.7.tar.xz

export PKG_CONFIG_PATH="$output/lib/pkgconfig:$(dirname -- "$jpeg_pc")${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
export LD_LIBRARY_PATH="$output/lib"
cmake_common=(-DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$output" -DCMAKE_INSTALL_LIBDIR=lib '-DCMAKE_INSTALL_RPATH=$ORIGIN' -DBUILD_SHARED_LIBS=ON)
meson_common=(--prefix "$output" --libdir lib --buildtype release --wrap-mode nodownload)

meson setup "$work/dav1d-build" "$work/dav1d-1.5.4" "${meson_common[@]}" \
  -Denable_tests=false -Denable_tools=false -Denable_examples=false
ninja -C "$work/dav1d-build" -j "$jobs" install
cmake -S "$work/highway-1.4.0" -B "$work/highway-build" "${cmake_common[@]}" \
  -DHWY_ENABLE_TESTS=OFF -DHWY_ENABLE_EXAMPLES=OFF -DHWY_ENABLE_CONTRIB=OFF
cmake --build "$work/highway-build" --parallel "$jobs"
cmake --install "$work/highway-build"
cmake -S "$work/libheif-1.23.7" -B "$work/heif-build" "${cmake_common[@]}" \
  -DENABLE_PLUGIN_LOADING=OFF -DENABLE_PARALLEL_TILE_DECODING=OFF \
  -DWITH_DAV1D=ON -DWITH_DAV1D_PLUGIN=OFF -DWITH_LIBDE265=OFF -DWITH_X265=OFF \
  -DWITH_X264=OFF -DWITH_OpenH264_DECODER=OFF -DWITH_AOM_DECODER=OFF -DWITH_AOM_ENCODER=OFF \
  -DWITH_LIBSHARPYUV=OFF -DWITH_EXAMPLES=OFF -DWITH_GDK_PIXBUF=OFF \
  -DBUILD_TESTING=OFF -DBUILD_DOCUMENTATION=OFF
cmake --build "$work/heif-build" --parallel "$jobs"
cmake --install "$work/heif-build"
meson setup "$work/vips-build" "$work/vips-8.18.7" "${meson_common[@]}" \
  -Dauto_features=disabled -Dmodules=disabled -Dheif=enabled -Dheif-module=disabled \
  -Djpeg=enabled -Dpng=enabled -Dwebp=enabled -Dexif=enabled -Dlcms=enabled \
  -Dhighway=enabled -Dzlib=enabled -Dintrospection=disabled -Dvapi=false \
  -Dcplusplus=false -Ddeprecated=false -Dexamples=false \
  '-Dc_link_args=-Wl,-rpath,$ORIGIN' '-Dcpp_link_args=-Wl,-rpath,$ORIGIN'
ninja -C "$work/vips-build" -j "$jobs" install

[[ "$(pkg-config --modversion vips)" == 8.18.7 ]] || fail 'Unexpected libvips version'
[[ "$(pkg-config --modversion libheif)" == 1.23.7 ]] || fail 'Unexpected libheif version'
mkdir -p "$output/share/licenses"
for source in dav1d-1.5.4 highway-1.4.0 libheif-1.23.7 vips-8.18.7; do
  for license in COPYING LICENSE LICENSE.txt; do
    if [[ -f "$work/$source/$license" ]]; then
      cp "$work/$source/$license" "$output/share/licenses/$source-$license"
    fi
  done
done
# The libvips license is also supplied with this repository's deployment notices.
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cp "$project/deploy/licenses/libvips-LICENSE.txt" "$output/share/licenses/libvips-LICENSE.txt"
{
  printf 'vips=%s\nlibheif=%s\ndav1d=%s\nhighway=%s\n' \
    "$(pkg-config --modversion vips)" "$(pkg-config --modversion libheif)" \
    "$(pkg-config --modversion dav1d)" "$(pkg-config --modversion libhwy)"
  printf 'jpeg=Debian libjpeg62-turbo\n'
} > "$output/build-info.txt"
printf 'Native image libraries prepared: %s\n' "$output"
