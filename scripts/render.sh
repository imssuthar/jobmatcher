#!/usr/bin/env bash
# Renders config templates into .run/ using values from .env.
set -euo pipefail
. "$(dirname "$0")/env.sh"
mkdir -p "$ROOT/.run"
sed -e "s|__MODEL_SMART__|$MODEL_SMART|g" \
    -e "s|__MODEL_FAST__|$MODEL_FAST|g" \
    -e "s|__MODEL_EMBED__|$MODEL_EMBED|g" \
    "$ROOT/deploy/litellm/config.tmpl.yaml" > "$ROOT/.run/litellm.yaml"
sed -e "s|__S3_ACCESS_KEY__|$S3_ACCESS_KEY|g" \
    -e "s|__S3_SECRET_KEY__|$S3_SECRET_KEY|g" \
    "$ROOT/deploy/seaweedfs/s3.tmpl.json" > "$ROOT/.run/s3.json"
