# Minimal end-to-end image: the claudex gateway plus the Claude Code CLI.
# Build from the repository root: docker build -f e2e/Dockerfile -t claudex-e2e .
FROM golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build
WORKDIR /source
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=e2e" -o /claudex ./cmd/claudex

FROM node:26.12.1-trixie-slim@sha256:36793bcbb160a6a16cede1e62b93f84e26b78a972a8a4c0b879c1c12f22761ec
ARG NPM_VERSION=12.2.1
ARG CLAUDE_CODE_VERSION=2.1.309
# npm 12 requires an explicit allowance for Claude's native-binary postinstall.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git ripgrep procps \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g "npm@${NPM_VERSION}" \
    && npm install -g --allow-scripts=@anthropic-ai/claude-code "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
    && claude --version \
    && npm cache clean --force \
    && mkdir -p /work /home/e2e && chmod 777 /work /home/e2e
COPY --from=build /claudex /usr/local/bin/claudex
COPY e2e/entrypoint.sh /usr/local/bin/e2e-entrypoint
RUN chmod 755 /usr/local/bin/e2e-entrypoint
ENV HOME=/home/e2e
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/e2e-entrypoint"]
