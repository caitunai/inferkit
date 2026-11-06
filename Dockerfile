# syntax=docker/dockerfile:1

ARG WITH_VIPS=true

FROM node:24-trixie-slim AS ui
ARG TARGETARCH
RUN test "$TARGETARCH" = amd64
WORKDIR /src/storage/ui
COPY storage/ui/package.json storage/ui/package-lock.json ./
RUN npm ci
COPY storage/ui/ ./
COPY storage/views/ /src/storage/views/
RUN npm run build

FROM debian:trixie-slim AS onnxruntime
ARG TARGETARCH
RUN test "$TARGETARCH" = amd64 \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl python3 \
    && rm -rf /var/lib/apt/lists/*
COPY scripts/prepare-onnxruntime-linux.sh /tmp/prepare-onnxruntime-linux.sh
RUN bash /tmp/prepare-onnxruntime-linux.sh /opt/onnxruntime
COPY scripts/prepare-openvino-linux.py /tmp/prepare-openvino-linux.py
RUN python3 /tmp/prepare-openvino-linux.py /opt/openvino

FROM golang:1.27-trixie AS build
ARG WITH_VIPS
ARG TARGETARCH
RUN test "$TARGETARCH" = amd64
RUN case "$WITH_VIPS" in \
      true) apt-get update && apt-get install -y --no-install-recommends \
        cmake meson ninja-build nasm pkg-config curl ca-certificates xz-utils \
        libglib2.0-dev libexpat1-dev libjpeg62-turbo-dev libpng-dev libwebp-dev \
        libexif-dev liblcms2-dev zlib1g-dev && rm -rf /var/lib/apt/lists/* ;; \
      false) ;; \
      *) exit 1 ;; \
    esac
WORKDIR /src
ENV CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GOAMD64=v1 GOEXPERIMENT=simd
ENV PKG_CONFIG_PATH=/opt/inferkit-image/lib/pkgconfig LD_LIBRARY_PATH=/opt/inferkit-image/lib
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/storage/ui/dist/ ./storage/ui/dist/
COPY --from=onnxruntime /opt/onnxruntime/include/ /opt/onnxruntime/include/
# Build pinned shared libvips/libheif/dav1d/Highway under this private prefix.
# dav1d is installed from source by this script, rather than by apt.
RUN if [ "$WITH_VIPS" = true ]; then bash scripts/prepare-image-linux.sh /opt/inferkit-image; else mkdir /opt/inferkit-image; fi
RUN image_tags=""; if [ "$WITH_VIPS" = true ]; then image_tags=vips; fi; \
    bash scripts/check-onnxruntime-api.sh /opt/onnxruntime \
    && go test -tags "$image_tags" ./... \
    && go build -tags "$image_tags" -trimpath -o /out/inferkit . \
    && go test -c -o /out/onnxruntime.test github.com/yalue/onnxruntime_go \
    && go test -c -tags "$image_tags" -o /out/ocr.test ./services/ocr \
    && if [ "$WITH_VIPS" = true ]; then \
         go test -c -tags vips -o /out/imagepipeline.test ./services/imagepipeline; \
       fi

FROM debian:trixie-slim AS runtime-base
ARG WITH_VIPS
ARG TARGETARCH
RUN test "$TARGETARCH" = amd64 \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl libstdc++6 libgcc-s1 locales passwd \
    && if [ "$WITH_VIPS" = true ]; then apt-get install -y --no-install-recommends \
         libglib2.0-0t64 libexpat1 libjpeg62-turbo libpng16-16t64 libwebp7 \
         libwebpmux3 libwebpdemux2 libexif12 liblcms2-2 zlib1g; fi \
    && localedef -i en_US -f UTF-8 en_US.UTF-8 \
    && groupadd --gid 10001 inferkit \
    && useradd --uid 10001 --gid 10001 --home-dir /var/lib/inferkit --no-create-home --shell /usr/sbin/nologin inferkit \
    && install -d -o inferkit -g inferkit /var/lib/inferkit \
    && install -d /etc/inferkit /models/artwork_fp32 /models/ocr \
    && rm -rf /var/lib/apt/lists/*
COPY --from=onnxruntime /opt/onnxruntime/lib/ /opt/onnxruntime/lib/
COPY --from=onnxruntime /opt/onnxruntime/LICENSE /opt/onnxruntime/ThirdPartyNotices.txt /usr/share/doc/onnxruntime/
COPY --from=onnxruntime /opt/openvino/ /opt/openvino/
COPY deploy/licenses/openvino-ep-LICENSE.txt /opt/openvino/licenses/LICENSE
COPY --from=build --chmod=0555 /out/inferkit /usr/local/bin/inferkit
# Includes libdav1d.so* and libheif.so*; LD_LIBRARY_PATH below resolves them.
COPY --from=build /opt/inferkit-image/ /opt/inferkit-image/
COPY deploy/licenses/png-LICENSE.txt deploy/licenses/libvips-LICENSE.txt /usr/share/doc/inferkit/
ENV HOME=/var/lib/inferkit LANG=en_US.UTF-8 PORT=8080 LD_LIBRARY_PATH=/opt/onnxruntime/lib:/opt/inferkit-image/lib:/opt/openvino/lib
WORKDIR /var/lib/inferkit
USER 10001:10001
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD curl --fail --silent --show-error --max-time 3 --output /dev/null "http://127.0.0.1:${PORT}/image-filter"
ENTRYPOINT ["/usr/local/bin/inferkit"]
CMD ["--config", "/etc/inferkit/app.toml", "image-filter", "serve"]

# Exercise the actual runtime libraries as the final non-root user. The test
# executable stays in this intermediate stage; only its success marker is copied.
FROM runtime-base AS runtime-check
COPY --from=build --chmod=0555 /out/ /tmp/verify/bin/
COPY services/imagefilter/testdata/ /tmp/verify/services/imagefilter/testdata/
COPY services/imagepipeline/testdata/ /tmp/verify/services/imagepipeline/testdata/
WORKDIR /tmp/verify/services/imagepipeline
RUN ONNXRUNTIME_SHARED_LIBRARY_PATH=/opt/onnxruntime/lib/libonnxruntime.so.1 \
    /tmp/verify/bin/onnxruntime.test -test.v -test.run='^(TestGetVersion|TestCreateTensor)$' \
    && /tmp/verify/bin/ocr.test -test.v \
         -test.run='^(TestSIMDProbabilitySemantics|TestCTCBlankRepeatsAndInvalidOutput|TestEqualWidthBatchingAndPixelContract|TestBatchCTCOrderAndInvalidRows)$' \
    && GODEBUG=cpu.avx=off /tmp/verify/bin/ocr.test -test.v \
         -test.run='^(TestSIMDProbabilitySemantics|TestCTCBlankRepeatsAndInvalidOutput)$' \
    && for library in /opt/openvino/lib/*.so*; do \
         dependencies=$(ldd "$library") || exit 1; \
         case "$dependencies" in *"not found"*) printf '%s\n' "$dependencies"; exit 1;; esac; \
       done \
    && if [ "$WITH_VIPS" = true ]; then \
         /tmp/verify/bin/imagepipeline.test -test.v \
           -test.run='^(TestNativeFormatsAndLimits|TestHighBitDepthAVIFMatchesTrainingPipeline|TestSmallAlphaAndCentering|TestJPEGObservedShrink|TestNativeParallelCancellationAndOwnership)$'; \
       fi \
    && printf 'ONNX Runtime C API, OCR kernels and image pipeline verified\n' > /tmp/runtime-verified

FROM runtime-base AS runtime
COPY --from=runtime-check /tmp/runtime-verified /usr/share/inferkit/runtime-verified
