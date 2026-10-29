# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.3-trixie AS builder
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

FROM debian:13.7-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium=156.0.8078.73-1~deb13u1 \
    chromium-common=156.0.8078.73-1~deb13u1 \
    chromium-sandbox=156.0.8078.73-1~deb13u1 \
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
