# syntax=docker/dockerfile:1.7
# Multi-stage build producing two images:
#   --target cms     web services, dispatcher, monitor, printing, cmsctl
#   --target worker  sandboxed judge with isolate and compilers

ARG GO_VERSION=1.27

FROM golang:${GO_VERSION}-trixie AS build
# Optional extra CA (TLS-intercepting corporate proxies): pass it as the
# build secret "extra_ca" (docker compose reads $CMS_EXTRA_CA_FILE).
RUN --mount=type=secret,id=extra_ca,required=false \
    if [ -s /run/secrets/extra_ca ]; then cp /run/secrets/extra_ca /usr/local/share/ca-certificates/extra.crt && update-ca-certificates >/dev/null; fi
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/D4ND3R/Contest-Management-System/internal/version.Version=${VERSION} -X github.com/D4ND3R/Contest-Management-System/internal/version.Commit=${COMMIT}" \
      -o /out/ ./cmd/cms ./cmd/cmsctl

# isolate (github.com/ioi/isolate) built from source, cgroup v2 mode.
FROM debian:trixie-slim AS isolate
ARG ISOLATE_VERSION=v2.7
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential ca-certificates git libcap-dev libseccomp-dev libsystemd-dev pkg-config \
    && rm -rf /var/lib/apt/lists/*
RUN --mount=type=secret,id=extra_ca,required=false \
    if [ -s /run/secrets/extra_ca ]; then cp /run/secrets/extra_ca /usr/local/share/ca-certificates/extra.crt && update-ca-certificates >/dev/null; fi
RUN git clone --depth 1 --branch ${ISOLATE_VERSION} https://github.com/ioi/isolate.git /isolate \
    && make -C /isolate isolate PREFIX=/usr/local VARPREFIX=/var/local CONFIGDIR=/usr/local/etc

FROM debian:trixie-slim AS cms
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata cups-client \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --home /var/lib/cms --create-home cms
COPY --from=build /out/cms /out/cmsctl /usr/local/bin/
COPY config/languages /etc/cms/languages
COPY deploy/docker/cms.yaml /etc/cms/cms.yaml
ENV CMS_CONFIG=/etc/cms/cms.yaml
USER cms
WORKDIR /var/lib/cms
ENTRYPOINT ["/usr/local/bin/cms"]

FROM debian:trixie-slim AS worker
# LANGS=minimal installs C, C++, Python 3 and Java; LANGS=full installs all
# twelve supported toolchains (several GB).
ARG LANGS=minimal
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates libcap2 libseccomp2 libsystemd0 gcc g++ libc6-dev python3 openjdk-21-jdk-headless \
    && if [ "$LANGS" = "full" ]; then apt-get install -y --no-install-recommends \
         pypy3 fp-compiler rustc golang-go kotlin mono-mcs ghc; fi \
    && rm -rf /var/lib/apt/lists/*
COPY --from=isolate /isolate/isolate /usr/local/bin/isolate
RUN chmod 4755 /usr/local/bin/isolate && mkdir -p /var/local/lib/isolate /run/isolate/locks
COPY deploy/docker/isolate.cf /usr/local/etc/isolate
COPY deploy/docker/worker-entrypoint.sh /usr/local/bin/worker-entrypoint.sh
COPY --from=build /out/cms /out/cmsctl /usr/local/bin/
COPY config/languages /etc/cms/languages
COPY deploy/docker/cms.yaml /etc/cms/cms.yaml
ENV CMS_CONFIG=/etc/cms/cms.yaml
ENTRYPOINT ["/usr/local/bin/worker-entrypoint.sh"]
CMD ["cms", "worker"]
