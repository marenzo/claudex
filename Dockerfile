FROM golang:1.26-alpine@sha256:ce864e7223ac17b1775e6fd0b4c0db580c2eb50e7953a427916379e4b92a1628 AS build
WORKDIR /source
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" -o /claudex ./cmd/claudex

FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
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
CMD ["-config", "/data/config.json", "-listen", "0.0.0.0:8317"]
