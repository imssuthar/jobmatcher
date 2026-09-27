# JobMatcher

An AI job-hunt copilot that runs **entirely on your machine** with local models:
upload your resume, get a structured profile and a searchable "fact bank" of your
achievements, then (in later phases) match jobs, tailor resumes and research companies.

No API keys and no cloud: one command starts everything, and one command proves it works.

## Quickstart

Prerequisites: **Docker** (with 6GB+ memory) and **make**. On macOS, Ollama runs natively
for Metal GPU acceleration; `make up` installs it with Homebrew if it is missing.

```bash
make up              # first run downloads ~7GB of models, then starts the stack
make verify-phase1   # self-checking acceptance tests with a pass/fail report
make demo RESUME=private/me.pdf   # process your own resume
```

Then open:

| URL | What |
|---|---|
| http://localhost:8080 | Upload UI: profile, fact bank, search, edit |
| http://localhost:6006 | Phoenix: every LLM call traced (prompt, output, tokens, latency) |
| http://localhost:4000 | LiteLLM gateway (OpenAI-compatible) |
| http://localhost:8888 | SeaweedFS filer: browse stored resume files |

Run `make` to list every target.

## Architecture (Phase 1)

```
 Browser / jmctl ──► core (Go) ──► Tika            (PDF/DOCX → text)
                        │    ──► SeaweedFS (S3)    (original files)
                        │    ──► Postgres+pgvector (profiles, facts, embeddings)
                        │    ──► LiteLLM gateway ──► Ollama (qwen2.5 7B/3B, nomic-embed-text)
                        └──────► Phoenix           (OpenTelemetry traces)
```

Resume pipeline (asynchronous, resumable after a crash):

```
upload ─► dedup (sha256) ─► store in S3 ─► parse (Tika)
       ─► extract profile (LLM, JSON schema, validate, retry with feedback)
       ─► extract facts   (LLM, JSON schema, validate, retry with feedback)
       ─► embed facts (nomic-embed-text, 768d) ─► activate profile version (one transaction)
```

Services only call **model aliases** (`smart`, `fast`, `embed`) on the gateway, so swapping
models is a change to `.env`, not to code.

## How "done" is proven

Every phase has three levels of tests:

| Command | Needs | What it checks |
|---|---|---|
| `make test` | Docker only | Unit tests with a fake LLM: validation, retries, pipeline states, dedup, HTTP |
| `make verify-phase1` | running stack | End-to-end checks against real local models, printed as a ✔/✘ report |
| `make eval-phase1` | running stack | Model quality comparison (fast vs smart), written to `docs/experiments/` |

LLM output is non-deterministic, so acceptance tests use temperature 0, a fixed seed and
**thresholds** (skill recall ≥ 80%, ≥ 90% of numbers in facts found in the resume) instead of
exact matches. See [docs/phases/phase-1.md](docs/phases/phase-1.md).

## Repository layout

```
Makefile, docker-compose.yml, .env.example   one-command stack
scripts/                                     doctor, up (Ollama + models + compose), report
deploy/litellm/                              gateway config template
services/core/                               Go service
  cmd/core        HTTP server + pipeline workers
  cmd/jmctl       CLI (used by make demo)
  cmd/eval        model comparison
  cmd/genfixtures builds PDF/DOCX fixtures from .txt sources
  internal/       platform, llm, extract, objstore, store, profile, evalkit, web
  tests/acceptance  phase acceptance tests
testdata/fixtures/   fictional resumes + golden answers
docs/                phases, ADRs, experiment reports
private/             your own resume (gitignored)
```

## Roadmap

| Phase | Deliverable | Status |
|---|---|---|
| 1 | Resume → structured profile + fact bank | ✅ |
| 2 | Job ingestion (Greenhouse/Lever), requirement extraction | |
| 3 | Match funnel: hybrid search → rerank → LLM gap analysis | |
| 4 | Temporal workflows, tracker, follow-up reminders | |
| 5 | Resume tailoring with truthfulness verifier | |
| 6 | MCP tools + research/outreach/interview agents | |
| 7 | Hardening, dashboards, semantic cache | |
