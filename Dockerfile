# syntax=docker/dockerfile:1

# Self-contained build: `docker build .` compiles avalanche and mtypes from
# source in this Dockerfile, no external promu/Makefile.common toolchain
# required. Cross-compiles for the requested target platform when built via
# `docker buildx build --platform=...`.

ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=unknown
ARG REVISION=unknown
ARG BRANCH=unknown
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/prometheus/common/version.Version=${VERSION} \
        -X github.com/prometheus/common/version.Revision=${REVISION} \
        -X github.com/prometheus/common/version.Branch=${BRANCH} \
        -X github.com/prometheus/common/version.BuildDate=${BUILD_DATE}" \
      -o /out/avalanche ./cmd/avalanche
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/mtypes ./cmd/mtypes

FROM quay.io/prometheus/busybox:latest
LABEL maintainer="The Prometheus Authors <prometheus-developers@googlegroups.com>"

COPY --from=builder /out/avalanche /bin/avalanche
COPY --from=builder /out/mtypes /bin/mtypes

EXPOSE      9001
USER        nobody
ENTRYPOINT  [ "/bin/avalanche" ]
