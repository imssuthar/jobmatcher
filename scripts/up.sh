#!/usr/bin/env bash
# Brings the whole stack up from nothing. Safe to run repeatedly.
set -euo pipefail
. "$(dirname "$0")/env.sh"

"$ROOT/scripts/doctor.sh"

wait_for() { # url, seconds, label
  local url=$1 secs=$2 label=$3
  for _ in $(seq "$secs"); do
    if curl -sf "$url" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  fail "$label did not become ready at $url"; return 1
}

step "Preparing Ollama ($OLLAMA_MODE)"
mkdir -p "$ROOT/.run"
if [ "$OLLAMA_MODE" = "native" ]; then
  if ! command -v ollama >/dev/null 2>&1; then
    echo "Installing Ollama with Homebrew..."
    brew install ollama
  fi
  if ! curl -sf "$OLLAMA_HOST_URL/api/version" >/dev/null 2>&1; then
    echo "Starting Ollama..."
    if [ -d /Applications/Ollama.app ]; then open -a Ollama; else nohup ollama serve >"$ROOT/.run/ollama.log" 2>&1 & fi
  fi
  wait_for "$OLLAMA_HOST_URL/api/version" 60 "Ollama"
  pull() { ollama pull "$1"; }
  have() { ollama list | awk 'NR>1 {print $1}' | grep -qx "$1" || ollama list | awk 'NR>1 {print $1}' | grep -qx "$1:latest"; }
else
  docker compose up -d --wait ollama
  pull() { docker compose exec -T ollama ollama pull "$1"; }
  have() { docker compose exec -T ollama ollama list | awk 'NR>1 {print $1}' | grep -qx "$1" || docker compose exec -T ollama ollama list | awk 'NR>1 {print $1}' | grep -qx "$1:latest"; }
fi
ok "Ollama is running"

step "Pulling models (first run downloads about 7GB)"
for m in "$MODEL_EMBED" "$MODEL_FAST" "$MODEL_SMART"; do
  if have "$m"; then ok "$m already present"; else pull "$m"; ok "$m pulled"; fi
done

step "Rendering configs"
"$ROOT/scripts/render.sh"
ok "smart=$MODEL_SMART fast=$MODEL_FAST embed=$MODEL_EMBED"

step "Starting services"
docker compose up -d --build --wait
ok "all services healthy"

step "Warming up models (first load into memory)"
for alias in smart embed; do
  if [ "$alias" = "embed" ]; then
    body='{"model":"embed","input":["warm up"]}'; path=embeddings
  else
    body='{"model":"'$alias'","messages":[{"role":"user","content":"Reply with OK"}],"max_tokens":5}'; path=chat/completions
  fi
  if curl -sf -m 300 "http://localhost:$LITELLM_PORT/v1/$path" \
       -H "Authorization: Bearer $LITELLM_MASTER_KEY" -H 'Content-Type: application/json' -d "$body" >/dev/null; then
    ok "$alias responds"
  else
    warn "$alias did not respond yet; check 'make logs'"
  fi
done

cat <<EOF

${c_green}JobMatcher is up.${c_off}
  App (upload UI)   http://localhost:$CORE_PORT
  LLM traces        http://localhost:$PHOENIX_PORT
  LLM gateway       http://localhost:$LITELLM_PORT
  Stored files      http://localhost:$S3_UI_PORT

Next:  make verify-phase1      (self-checking acceptance tests)
       make demo RESUME=private/me.pdf
EOF
