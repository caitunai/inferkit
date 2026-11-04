# syntax=docker/dockerfile:1

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
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY scripts/prepare-onnxruntime-linux.sh /tmp/prepare-onnxruntime-linux.sh
RUN bash /tmp/prepare-onnxruntime-linux.sh /opt/onnxruntime

FROM golang:1.27-trixie AS build
ARG TARGETARCH
RUN test "$TARGETARCH" = amd64
WORKDIR /src
ENV CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GOAMD64=v1
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/storage/ui/dist/ ./storage/ui/dist/
COPY --from=onnxruntime /opt/onnxruntime/include/ /opt/onnxruntime/include/
RUN bash scripts/check-onnxruntime-api.sh /opt/onnxruntime \
    && go test ./... \
    && go build -trimpath -o /out/inferkit . \
    && go test -c -o /out/onnxruntime.test github.com/yalue/onnxruntime_go

FROM debian:trixie-slim AS runtime-base
ARG TARGETARCH
RUN test "$TARGETARCH" = amd64 \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl libstdc++6 libgcc-s1 locales passwd \
    && localedef -i en_US -f UTF-8 en_US.UTF-8 \
    && groupadd --gid 10001 inferkit \
    && useradd --uid 10001 --gid 10001 --home-dir /var/lib/inferkit --no-create-home --shell /usr/sbin/nologin inferkit \
    && install -d -o inferkit -g inferkit /var/lib/inferkit \
    && install -d /etc/inferkit /models/artwork_fp32 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=onnxruntime /opt/onnxruntime/lib/ /opt/onnxruntime/lib/
COPY --from=onnxruntime /opt/onnxruntime/LICENSE /opt/onnxruntime/ThirdPartyNotices.txt /usr/share/doc/onnxruntime/
COPY --from=build --chmod=0555 /out/inferkit /usr/local/bin/inferkit
ENV LANG=en_US.UTF-8 PORT=8080 LD_LIBRARY_PATH=/opt/onnxruntime/lib
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
COPY --from=build --chmod=0555 /out/onnxruntime.test /tmp/onnxruntime.test
RUN ONNXRUNTIME_SHARED_LIBRARY_PATH=/opt/onnxruntime/lib/libonnxruntime.so.1 \
    /tmp/onnxruntime.test -test.v -test.run='^(TestGetVersion|TestCreateTensor)$' \
    && printf 'ONNX Runtime C API and tensors verified\n' > /tmp/runtime-verified

FROM runtime-base AS runtime
COPY --from=runtime-check /tmp/runtime-verified /usr/share/inferkit/runtime-verified
