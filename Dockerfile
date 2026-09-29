# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

FROM --platform=$BUILDPLATFORM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS build

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

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

LABEL org.opencontainers.image.title="rungar" \
      org.opencontainers.image.description="GitHub Actions runners as virtual machines, one per job, on hosts of your own" \
      org.opencontainers.image.source="https://github.com/konradasb/rungar" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/rungar /usr/local/bin/rungar
COPY --from=build --chown=nonroot:nonroot /out/run/rungar /run/rungar
COPY --from=build --chown=nonroot:nonroot /out/var/log/rungar /var/log/rungar

VOLUME /var/log/rungar

USER nonroot:nonroot

EXPOSE 9102

ENTRYPOINT ["/usr/local/bin/rungar"]
CMD ["serve", "--config", "/etc/rungar/config.yaml"]
