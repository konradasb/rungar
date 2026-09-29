# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

#
# Rungar is a network client: it talks to Dicer daemons over gRPC and to GitHub
# over HTTPS, and needs nothing of the machine it runs on. A container is the
# tidiest way to run it somewhere that is not a machine you keep binaries on.
#
#   make docker
#
# Released images are built by .github/workflows/release.yaml, for linux/amd64
# and linux/arm64, and published as ghcr.io/konradasb/rungar.
#
# The base images are pinned by digest as well as by tag, so that what a tag
# points at cannot change underneath a build; Dependabot moves both.

# The build runs on the builder's own platform and cross-compiles, which Go
# does natively: no emulation, however many platforms are asked for.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/
COPY proto/ proto/

ARG TARGETOS TARGETARCH
ARG VERSION=0.0.0-dev
ARG COMMIT=""
ARG BUILD_DATE=1970-01-01T00:00:00Z

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w \
        -X github.com/konradasb/rungar/internal/version.Version=${VERSION} \
        -X github.com/konradasb/rungar/internal/version.Commit=${COMMIT} \
        -X github.com/konradasb/rungar/internal/version.BuildDate=${BUILD_DATE}" \
      -o /out/rungar ./cmd/rungar \
 && mkdir -p /out/run/rungar /out/var/log/rungar

# Nothing but the binary, a certificate store and a passwd file: Rungar opens no
# files but its configuration's and its events, runs nothing, and has no use
# for a shell. The
# certificates are what let it verify GitHub; without them every call fails.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

LABEL org.opencontainers.image.title="rungar" \
      org.opencontainers.image.description="GitHub Actions runners as virtual machines, one per job, on hosts of your own" \
      org.opencontainers.image.source="https://github.com/konradasb/rungar" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/rungar /usr/local/bin/rungar

# Where the daemon serves the command line, so that `docker exec rungar rungar
# status` finds it.
COPY --from=build --chown=nonroot:nonroot /out/run/rungar /run/rungar

# Where the daemon keeps its events, so that `rungar events` has them after a
# restart. What it knows of runners is read back from the fleet on every
# start; the events are history, and a volume keeps them.
COPY --from=build --chown=nonroot:nonroot /out/var/log/rungar /var/log/rungar
VOLUME /var/log/rungar

# The configuration, and whatever credentials it names, are mounted read-only,
# the socket's directory is a tmpfs of its own, and the events a volume:
#   docker run --read-only \
#     -v /etc/rungar:/etc/rungar:ro \
#     -v rungar-events:/var/log/rungar \
#     --tmpfs /run/rungar:uid=65532,gid=65532,mode=0750 \
#     ghcr.io/konradasb/rungar
USER nonroot:nonroot

EXPOSE 9102

ENTRYPOINT ["/usr/local/bin/rungar"]
CMD ["serve", "--config", "/etc/rungar/config.yaml"]
