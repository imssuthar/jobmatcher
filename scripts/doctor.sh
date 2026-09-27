#!/usr/bin/env bash
# Checks the machine can run JobMatcher. Exits non-zero on a hard failure.
set -uo pipefail
. "$(dirname "$0")/env.sh"

errors=0
step "Checking prerequisites"

if command -v docker >/dev/null 2>&1; then ok "docker installed"; else fail "docker not installed: https://docs.docker.com/get-docker/"; errors=$((errors+1)); fi

if docker info >/dev/null 2>&1; then
  ok "docker daemon running"
  mem_gb=$(( $(docker info --format '{{.MemTotal}}') / 1024 / 1024 / 1024 ))
  if [ "$mem_gb" -ge 6 ]; then ok "docker memory ${mem_gb}GB"; else warn "docker has ${mem_gb}GB memory; give it at least 6GB (Docker Desktop > Settings > Resources)"; fi
else
  fail "docker daemon not running (start Docker Desktop)"; errors=$((errors+1))
fi

if docker compose version >/dev/null 2>&1; then
  ok "docker compose $(docker compose version --short)"
else
  fail "docker compose v2 not available"; errors=$((errors+1))
fi

if [ "$OLLAMA_MODE" = "native" ]; then
  if command -v ollama >/dev/null 2>&1; then
    ok "ollama installed ($(ollama --version 2>/dev/null | awk '{print $NF}' | tail -1))"
  elif command -v brew >/dev/null 2>&1; then
    warn "ollama not installed; 'make up' will install it with Homebrew"
  else
    fail "ollama not installed: https://ollama.com/download"; errors=$((errors+1))
  fi
else
  ok "ollama will run in a container (mode: container)"
fi

free_gb=$(df -Pk "$ROOT" | awk 'NR==2 {print int($4/1024/1024)}')
if [ "$free_gb" -ge 15 ]; then ok "disk free ${free_gb}GB"; else warn "only ${free_gb}GB free; models and images need about 15GB"; fi

running=$(docker compose ps -q 2>/dev/null | wc -l | tr -d ' ')
if [ "$running" = "0" ]; then
  for p in "$CORE_PORT" "$LITELLM_PORT" "$PHOENIX_PORT" "$POSTGRES_PORT" "$S3_UI_PORT"; do
    if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
      fail "port $p is already in use by another process (change it in .env)"; errors=$((errors+1))
    fi
  done
fi

if [ "$errors" -gt 0 ]; then
  fail "$errors problem(s) found"
  exit 1
fi
ok "machine is ready"
