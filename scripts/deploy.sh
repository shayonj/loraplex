#!/usr/bin/env bash
set -euo pipefail

REMOTE_HOST="${1:?Usage: deploy.sh <user@host>}"
REMOTE_DIR="/opt/loraplex"

echo "==> Cross-compiling for linux/amd64..."
cd "$(dirname "$0")/.."
make build-linux

echo "==> Syncing to ${REMOTE_HOST}:${REMOTE_DIR}..."
ssh "$REMOTE_HOST" "mkdir -p ${REMOTE_DIR}"
scp bin/loraplex-linux-amd64 "$REMOTE_HOST:${REMOTE_DIR}/loraplex"
scp config.example.yaml "$REMOTE_HOST:${REMOTE_DIR}/config.yaml"
ssh "$REMOTE_HOST" "chmod +x ${REMOTE_DIR}/loraplex"

echo "==> Creating cache dirs..."
ssh "$REMOTE_HOST" "mkdir -p /dev/shm/loraplex /mnt/nvme/loraplex 2>/dev/null || mkdir -p /dev/shm/loraplex /tmp/loraplex_nvme"

echo "==> Starting loraplex..."
ssh "$REMOTE_HOST" "${REMOTE_DIR}/loraplex --config ${REMOTE_DIR}/config.yaml &"

sleep 2
echo "==> Health check..."
ssh "$REMOTE_HOST" "curl -s http://localhost:9090/healthz"
echo ""

echo "==> Done. loraplex running on ${REMOTE_HOST}:9090"
