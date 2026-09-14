#!/bin/sh
# Start the gateway against the mounted credential directory, wait until it is
# healthy, then idle so the test can drive `claude` with docker exec.
set -eu
mkdir -p "$HOME"
claudex -config /data/config.json &
gateway=$!
for _ in $(seq 1 100); do
  if curl -fsS -o /dev/null http://127.0.0.1:8317/healthz; then
    echo "claudex e2e gateway ready"
    wait "$gateway"
    exit $?
  fi
  if ! kill -0 "$gateway" 2>/dev/null; then
    echo "claudex exited before becoming healthy" >&2
    exit 1
  fi
  sleep 0.2
done
echo "claudex did not become healthy" >&2
exit 1
