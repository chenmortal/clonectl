#!/usr/bin/env bash
# scripts/s3_append_e2e.sh — provision real rclone + MinIO, then run the
# appendable-object BadDigest e2e (internal/services TestS3AppendSyncE2E).
#
# Proves, against real components, that:
#   1. appendable-object sources (reported ETag ≠ content digest) fail
#      PutObject with BadDigest — the production incident;
#   2. disable_checksum is a dead end (v1.75 single-part PutObject still
#      sends Content-Md5);
#   3. the recipe {upload_cutoff=0, ignore_checksum} syncs successfully
#      with byte-identical content (upload_cutoff rides the fs spec override,
#      the only channel where s3 backend options take effect);
#   4. min_age only syncs frozen objects.
#
# Usage:        scripts/s3_append_e2e.sh
# Env overrides:
#   RCLONE_BIN                       rclone binary (default: PATH or download)
#   MINIO_ENDPOINT                   existing MinIO host:port (skips provisioning)
#   MINIO_ACCESS_KEY/MINIO_SECRET_KEY  root creds for that instance
#   MINIO_PORT / MINIO_CONSOLE_PORT  local ports for the provisioned server
#   MINIO_BIN                        cached MinIO binary path

set -euo pipefail
cd "$(dirname "$0")/.."

# keep a full copy of the run output for post-mortem (the console gets it too)
CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/clonectl-e2e"
mkdir -p "$CACHE_DIR"
exec > >(tee "$CACHE_DIR/last-run.log") 2>&1

# --- rclone binary ---------------------------------------------------------
if [[ -z "${RCLONE_BIN:-}" ]] && ! command -v rclone >/dev/null 2>&1; then
  RCLONE_BIN="$CACHE_DIR/rclone"
  if [[ ! -x "$RCLONE_BIN" ]]; then
    case "$(uname -m)" in
      x86_64)          RC_ARCH="amd64" ;;
      aarch64 | arm64) RC_ARCH="arm64" ;;
      *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
    esac
    DL="$(mktemp -d)"
    echo "==> downloading rclone (linux-${RC_ARCH}) ..."
    curl -fsSL "https://downloads.rclone.org/rclone-current-linux-${RC_ARCH}.zip" -o "$DL/rclone.zip"
    unzip -q "$DL/rclone.zip" -d "$DL"
    found="$(find "$DL" -type f -name rclone | head -n1)"
    mv "$found" "$RCLONE_BIN"
    chmod +x "$RCLONE_BIN"
  fi
  export RCLONE_BIN
fi
echo "==> rclone: ${RCLONE_BIN:-$(command -v rclone)}"

# --- MinIO -----------------------------------------------------------------
if [[ -n "${MINIO_ENDPOINT:-}" ]]; then
  if [[ -z "${MINIO_ACCESS_KEY:-}" || -z "${MINIO_SECRET_KEY:-}" ]]; then
    echo "MINIO_ENDPOINT is set but MINIO_ACCESS_KEY/MINIO_SECRET_KEY missing" >&2
    exit 1
  fi
  export MINIO_ACCESS_KEY MINIO_SECRET_KEY
else
  MINIO_PORT="${MINIO_PORT:-29000}"
  MINIO_CONSOLE_PORT="${MINIO_CONSOLE_PORT:-29001}"
  MINIO_ACCESS_KEY="e2e$(head -c 6 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  MINIO_SECRET_KEY="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  export MINIO_ACCESS_KEY MINIO_SECRET_KEY

  if ss -tln 2>/dev/null | awk '{print $4}' | grep -qE ":((${MINIO_PORT})|(${MINIO_CONSOLE_PORT}))\$"; then
    echo "port ${MINIO_PORT}/${MINIO_CONSOLE_PORT} busy — set MINIO_PORT/MINIO_CONSOLE_PORT or stop the occupant" >&2
    exit 1
  fi

  WORK="$(mktemp -d)"
  mkdir -p "$WORK/data"
  MINIO_PID=""
  cleanup() {
    [[ -n "$MINIO_PID" ]] && kill "$MINIO_PID" 2>/dev/null || true
  }
  trap cleanup EXIT

  # 1) rustfs — MinIO-compatible S3 server (AIStor MinIO binaries refuse
  #    writes without a license, so rustfs is the binary path). Cached.
  RS_ARCH="$(uname -m)"
  case "$RS_ARCH" in
    x86_64)          RS_ARCH="x86_64" ;;
    aarch64 | arm64) RS_ARCH="aarch64" ;;
    *) echo "unsupported arch: $RS_ARCH" >&2; exit 1 ;;
  esac
  RUSTFS_BIN="${RUSTFS_BIN:-$CACHE_DIR/rustfs}"
  if [[ ! -x "$RUSTFS_BIN" ]]; then
    echo "==> downloading rustfs (linux-${RS_ARCH}, cached at $RUSTFS_BIN) ..."
    if curl -fsSL "https://github.com/rustfs/rustfs/releases/latest/download/rustfs-linux-${RS_ARCH}-gnu-latest.zip" \
        -o "$WORK/rustfs.zip"; then
      unzip -qo "$WORK/rustfs.zip" -d "$WORK/rustfs"
      found="$(find "$WORK/rustfs" -type f -name 'rustfs' | head -n1)"
      if [[ -n "$found" ]]; then
        mkdir -p "$(dirname "$RUSTFS_BIN")"
        mv "$found" "$RUSTFS_BIN"
      fi
    fi
  fi
  [[ -x "$RUSTFS_BIN" ]] && chmod +x "$RUSTFS_BIN"

  if [[ -x "$RUSTFS_BIN" ]]; then
    echo "==> starting rustfs on 127.0.0.1:${MINIO_PORT} ..."
    RUSTFS_ACCESS_KEY="$MINIO_ACCESS_KEY" RUSTFS_SECRET_KEY="$MINIO_SECRET_KEY" \
      "$RUSTFS_BIN" server "$WORK/data" \
      --address "127.0.0.1:${MINIO_PORT}" \
      --console-address "127.0.0.1:${MINIO_CONSOLE_PORT}" \
      >"$WORK/rustfs.log" 2>&1 &
    MINIO_PID=$!
  else
    # 2) locally cached docker image only (docker.io is unreachable here)
    if docker image inspect minio/minio >/dev/null 2>&1; then
      docker rm -f clonectl-s3-e2e-minio >/dev/null 2>&1 || true
      echo "==> starting MinIO (cached docker image) on 127.0.0.1:${MINIO_PORT} ..."
      docker run -d --rm --name clonectl-s3-e2e-minio \
        -p "127.0.0.1:${MINIO_PORT}:9000" \
        -e "MINIO_ROOT_USER=${MINIO_ACCESS_KEY}" \
        -e "MINIO_ROOT_PASSWORD=${MINIO_SECRET_KEY}" \
        minio/minio server /data >/dev/null
    else
      echo "cannot provision an S3 server (no rustfs download, no cached minio image)" >&2
      exit 1
    fi
  fi

  echo "==> waiting for S3 server on 127.0.0.1:${MINIO_PORT} ..."
  ok=0
  for _ in $(seq 1 120); do
    if curl -fsS "http://127.0.0.1:${MINIO_PORT}/minio/health/live" >/dev/null 2>&1; then
      ok=1
      break
    fi
    sleep 1
  done
  if [[ "$ok" -ne 1 ]]; then
    echo "S3 server did not become healthy; logs:" >&2
    tail -n 30 "$WORK"/*.log 2>/dev/null || true
    exit 1
  fi
  MINIO_ENDPOINT="127.0.0.1:${MINIO_PORT}"
  export MINIO_ENDPOINT
fi

# --- the e2e ----------------------------------------------------------------
echo "==> running TestS3AppendSyncE2E (rclone=${RCLONE_BIN:-$(command -v rclone)}, minio=${MINIO_ENDPOINT})"
go test ./internal/services -run TestS3AppendSyncE2E -v -count=1 -timeout 15m
