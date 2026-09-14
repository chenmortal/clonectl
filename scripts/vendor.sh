#!/usr/bin/env bash
# Vendor the project's runtime dependencies to a local directory and optionally
# upload them to a Nexus pip proxy.
#
# Subcommands:
#   download   export poetry.lock -> requirements.txt -> pip download -> vendor/wheels/
#   upload     upload vendor/wheels/*.whl to $NEXUS_URL via twine
#   all        download then upload (default)
#   clean      remove vendor/ and requirements.txt
#
# Environment variables (read where used):
#   NEXUS_URL          Nexus pypi-hosted repo URL (required for upload)
#   NEXUS_USERNAME     basic-auth user (required for upload)
#   NEXUS_PASSWORD     basic-auth password (required for upload)
#   OUTPUT_DIR         vendor directory (default: vendor/wheels)
#   PLATFORMS          space-separated platform tags for pip --platform
#   PYTHON_VERSION     PEP 425 python tag, e.g. 310 (default: 310)
#   SKIP_UPLOAD        set to 1 to skip upload in 'all'

set -euo pipefail

cd "$(dirname "$0")/.."

OUTPUT_DIR="${OUTPUT_DIR:-vendor/wheels}"
PYTHON_VERSION="${PYTHON_VERSION:-310}"
PLATFORMS="${PLATFORMS:-manylinux_2_17_x86_64 manylinux_2_17_aarch64 win_amd64}"

usage() {
  cat <<EOF
Usage: $0 <command>

Commands:
  download   Export poetry.lock -> requirements.txt -> pip download to vendor/wheels
  upload     Upload vendor/wheels/*.whl to Nexus via twine
  all        download then upload (default)
  clean      Remove vendor/ and requirements.txt

Environment:
  NEXUS_URL, NEXUS_USERNAME, NEXUS_PASSWORD    Nexus credentials (upload only)
  OUTPUT_DIR=${OUTPUT_DIR}
  PYTHON_VERSION=${PYTHON_VERSION}
  PLATFORMS="${PLATFORMS}"
  SKIP_UPLOAD=1  skip upload in 'all'

Notes:
  - 'download' always wipes vendor/ first to avoid stale artifacts (e.g. a
    self-referential sdist that includes the project's own DB / logs).
  - Uses 'poetry export' (not 'pip freeze'), so dev-group deps like poetry,
    mypy, ruff, pytest are never exported.
  - '--only-binary=:all:' forces wheels; the internal-network target hosts do
    not need a C compiler.
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "ERROR: required command not found: $1" >&2
    exit 1
  fi
}

cmd_download() {
  require_cmd poetry
  require_cmd pip

  rm -rf vendor
  mkdir -p "$OUTPUT_DIR"

  echo ">> exporting poetry.lock -> requirements.txt (runtime deps only)"
  poetry export \
    --format requirements.txt \
    --output requirements.txt \
    --without-hashes

  local platform_args=()
  for p in $PLATFORMS; do
    platform_args+=(--platform "$p")
  done

  echo ">> pip download into $OUTPUT_DIR"
  echo "   platforms: $PLATFORMS"
  echo "   python:    $PYTHON_VERSION"

  pip download \
    -r requirements.txt \
    -d "$OUTPUT_DIR" \
    --python-version "$PYTHON_VERSION" \
    --only-binary=:all: \
    "${platform_args[@]}"

  local count
  count=$(find "$OUTPUT_DIR" -maxdepth 1 -name '*.whl' | wc -l | tr -d ' ')
  echo ">> done: $count wheel(s) in $OUTPUT_DIR"
}

cmd_upload() {
  require_cmd twine

  if [ ! -d "$OUTPUT_DIR" ]; then
    echo "ERROR: $OUTPUT_DIR not found. Run '$0 download' first." >&2
    exit 1
  fi
  if [ -z "${NEXUS_URL:-}" ]; then
    echo "ERROR: NEXUS_URL env var required for upload." >&2
    exit 1
  fi
  if [ -z "${NEXUS_USERNAME:-}" ] || [ -z "${NEXUS_PASSWORD:-}" ]; then
    echo "ERROR: NEXUS_USERNAME and NEXUS_PASSWORD env vars required for upload." >&2
    exit 1
  fi

  local wheels=()
  while IFS= read -r w; do wheels+=("$w"); done < <(find "$OUTPUT_DIR" -maxdepth 1 -name '*.whl')
  if [ ${#wheels[@]} -eq 0 ]; then
    echo "ERROR: no wheels found in $OUTPUT_DIR" >&2
    exit 1
  fi

  echo ">> uploading ${#wheels[@]} wheel(s) to $NEXUS_URL"
  twine upload \
    --repository-url "$NEXUS_URL" \
    --username "$NEXUS_USERNAME" \
    --password "$NEXUS_PASSWORD" \
    "${wheels[@]}"
}

cmd_clean() {
  rm -rf vendor requirements.txt
  echo ">> cleaned vendor/ and requirements.txt"
}

cmd="${1:-all}"
case "$cmd" in
  download) cmd_download ;;
  upload)   cmd_upload ;;
  all)
    cmd_download
    if [ "${SKIP_UPLOAD:-0}" != "1" ]; then
      cmd_upload
    else
      echo ">> SKIP_UPLOAD=1, skipping upload"
    fi
    ;;
  clean)    cmd_clean ;;
  -h|--help|help) usage ;;
  *)
    usage
    exit 2
    ;;
esac
