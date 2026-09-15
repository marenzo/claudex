# Minimal end-to-end image: the claudex gateway plus the Claude Code CLI.
# Build from the repository root: docker build -f e2e/Dockerfile -t claudex-e2e .
FROM golang:1.27-alpine AS build
WORKDIR /source
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=e2e" -o /claudex ./cmd/claudex

FROM node:24-bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git ripgrep procps \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code \
    && npm cache clean --force \
    && mkdir -p /work /home/e2e && chmod 777 /work /home/e2e
COPY --from=build /claudex /usr/local/bin/claudex
COPY e2e/entrypoint.sh /usr/local/bin/e2e-entrypoint
RUN chmod 755 /usr/local/bin/e2e-entrypoint
ENV HOME=/home/e2e
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/e2e-entrypoint"]
