# ADR 0001: Native Ollama on macOS, container on Linux

**Status:** accepted

## Context

The whole stack should start with `make up`. Everything else runs in Docker, but Docker on
macOS runs inside a Linux VM with no access to the Apple GPU (Metal). A 7B model on CPU in
that VM is roughly 5–10× slower than native Ollama on Apple Silicon.

## Decision

- `OLLAMA_MODE=auto` (default): **native** Ollama on macOS, an **ollama container** on Linux.
- On macOS, `make up` installs Ollama with Homebrew if needed, starts it, and containers
  reach it at `host.docker.internal:11434`.
- On Linux the `ollama` compose profile is enabled and models are pulled inside the container.
- Services never talk to Ollama directly; they call the LiteLLM gateway, which is the only
  component that knows where Ollama lives.

## Consequences

- One host dependency beyond Docker on macOS (Ollama), installed automatically.
- Model files live in the user's normal Ollama store on macOS and are shared with other tools.
- Linux GPU passthrough (NVIDIA) is not configured yet; Linux runs on CPU unless the
  compose service is extended with GPU settings.
