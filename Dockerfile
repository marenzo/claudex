FROM golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build
WORKDIR /source
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" -o /claudex ./cmd/claudex

FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
LABEL org.opencontainers.image.source="https://github.com/marenzo/claudex" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.description="Local gateway that runs Claude Code on GPT models through a Codex subscription"
RUN apk add --no-cache ca-certificates && addgroup -g 10001 claudex && adduser -D -H -u 10001 -G claudex claudex
COPY --from=build /claudex /usr/local/bin/claudex
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/claudex/
USER 10001:10001
VOLUME /data
EXPOSE 8317
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD wget -q -O /dev/null http://127.0.0.1:8317/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/claudex"]
CMD ["ctl", "run", "-config", "/data/config.json", "-listen", "0.0.0.0:8317"]
