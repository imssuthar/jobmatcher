#!/usr/bin/env bash
# Sourced by every script: loads .env and decides how Ollama runs.
#   macOS  -> native Ollama (uses the Metal GPU; Docker on Mac cannot)
#   Linux  -> Ollama container (compose profile "ollama")

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

[ -f .env ] || cp .env.example .env
set -a
# shellcheck disable=SC1091
. ./.env
set +a

c_green=$'\033[32m'; c_red=$'\033[31m'; c_yellow=$'\033[33m'; c_dim=$'\033[2m'; c_off=$'\033[0m'
ok()   { printf '%s✔%s %s\n' "$c_green" "$c_off" "$*"; }
warn() { printf '%s!%s %s\n' "$c_yellow" "$c_off" "$*"; }
fail() { printf '%s✘%s %s\n' "$c_red" "$c_off" "$*"; }
step() { printf '\n%s==>%s %s\n' "$c_dim" "$c_off" "$*"; }

mode="${OLLAMA_MODE:-auto}"
if [ "$mode" = "auto" ]; then
  if [ "$(uname -s)" = "Darwin" ]; then mode=native; else mode=container; fi
fi
export OLLAMA_MODE="$mode"

if [ "$OLLAMA_MODE" = "native" ]; then
  export OLLAMA_BASE_URL="http://host.docker.internal:11434"
  export OLLAMA_HOST_URL="http://localhost:11434"
  export COMPOSE_PROFILES=""
else
  export OLLAMA_BASE_URL="http://ollama:11434"
  export OLLAMA_HOST_URL=""
  export COMPOSE_PROFILES="ollama"
fi
