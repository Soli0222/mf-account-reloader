# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.2-trixie@sha256:e58d6f83b3416618d8bcac2b3dde1b7f7e3c4a77d25e88637f8bbae81536c48d AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -o /out/mf-account-reloader ./cmd/mf-account-reloader

FROM builder AS test-builder
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go test -c -tags=integration -o /out/integration.test ./tests/integration
FROM scratch AS test-artifacts
COPY --from=test-builder /out/integration.test /integration.test

FROM debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium=155.0.8059.79-1~deb13u1 \
    chromium-common=155.0.8059.79-1~deb13u1 \
    chromium-sandbox=155.0.8059.79-1~deb13u1 \
    ca-certificates fonts-noto-cjk tzdata tini \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 reloader \
    && useradd --uid 10001 --gid 10001 --home-dir /tmp/home --no-create-home reloader \
    && mkdir -p /state && chown 10001:10001 /state
COPY config.container.yaml /etc/mfr/config.yaml
ENV HOME=/tmp/home XDG_CACHE_HOME=/tmp/cache MFR_CONFIG=/etc/mfr/config.yaml
USER 10001:10001
WORKDIR /tmp
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/mf-account-reloader"]
CMD ["run"]

FROM runtime AS final
COPY --from=builder /out/mf-account-reloader /usr/local/bin/mf-account-reloader
