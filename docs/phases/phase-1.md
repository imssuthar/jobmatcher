# Phase 1: Profile and fact bank

## Goal

Turn an uploaded resume (PDF, DOCX, TXT/MD) into:

1. a **structured profile** (name, contact, skills, roles, education), versioned, and
2. a **fact bank**: atomic, verifiable statements about the candidate, each embedded for
   semantic search. Later phases match jobs against it (Phase 3) and may only tailor
   resumes from it (Phase 5).

## What was built

| Area | Implementation |
|---|---|
| Stack | Postgres 17 + pgvector, SeaweedFS (S3), Apache Tika, LiteLLM, Phoenix, native Ollama on macOS |
| Upload API | `POST /v1/resumes` → 202, async pipeline, SHA-256 dedup (failed uploads may retry) |
| Pipeline | queued → parsing → extracting_profile → extracting_facts → embedding → ready / failed |
| Extraction | schema-constrained JSON (`response_format: json_schema`), temperature 0, seed 42 |
| Validation | Go-side checks + grounding (name and email must appear in the text); on failure the errors go back to the model, up to 2 retries |
| Embeddings | nomic-embed-text 768d with `search_document:` / `search_query:` task prefixes, HNSW cosine index |
| Consistency | profile + facts activated in one transaction; a failed resume never changes the active profile |
| Recovery | unfinished resumes are re-queued on startup (at-least-once); shutdown never marks work failed |
| Read/edit API | `GET /v1/profile`, `/v1/profile/facts`, `/v1/profile/facts/search?q=`, `PATCH/DELETE /v1/profile/facts/{id}` (edits re-embed) |
| Observability | OpenTelemetry spans with OpenInference attributes (model, input, output, token counts) in Phoenix |
| UI | single embedded page at `/` |

## Done when

`make up && make verify-phase1` passes on a clean machine:

| # | Check | Threshold |
|---|---|---|
| 01 | Stack healthy; `smart`, `fast`, `embed` respond; embed returns 768 dims | all |
| 02 | Each fixture (PDF, DOCX, TXT) reaches `ready` | < 10 min each |
| 03 | Name and email exact; skill recall; company recall | skills ≥ 80%, companies 100% |
| 04 | Fact count in range; every fact embedded; valid categories; numbers grounded | ≥ 90% of numbers appear in the resume |
| 05 | Semantic search finds the expected fact in the top 3 | recall@3 ≥ 2/3 per resume; misses are printed |
| 06 | Re-uploading the same file deduplicates, active version unchanged | exact |
| 07 | Editing a fact re-embeds it; search returns it first; too-short edits rejected | exact |
| 08 | A corrupt PDF fails at `parsing` with an error; service stays healthy | exact |
| 09 | Unsupported type (PNG) is rejected with 415 | exact |
| 10 | LLM calls are visible as traces in Phoenix | > 0 traces |

And `make demo RESUME=private/me.pdf` shows your real profile and fact bank.

## Experiment

`make eval-phase1` compares `fast` (qwen2.5:3b) and `smart` (qwen2.5:7b) on the fixtures:
latency, retries, skill recall, skill grounding and number grounding. Results are in
[../experiments/phase1-extraction-models.md](../experiments/phase1-extraction-models.md).

## Findings (first run, M3 Pro 18GB)

- **All 18 acceptance checks pass.** Each resume takes about 60–67 s end to end with
  qwen2.5:7b (parse → profile → facts → embed). No run needed a correction retry.
- **3B vs 7B:** on these fixtures both models scored 100% skill recall, 100% grounding and
  0 retries; the 3B model was ~30% faster (43 s vs 62 s per resume). The fixtures are clean,
  well-formatted resumes, so they don't separate the models yet. Harder fixtures (two-column
  PDFs, messy formatting, non-English) are needed before switching `MODEL_FACTS` to `fast`.
- **Vector search misses paraphrases.** "continuous delivery automation" did not retrieve
  "Built CI/CD pipelines with Jenkins…" in the top 3 (recall@3 8/9 overall). This is the
  motivating case for hybrid search (BM25 + vector) and reranking in Phase 3.
- **Facts are shorter than the prompt asks.** Facts rarely name the company inside the text
  ("Mentored a team of 6 engineers…"), even though the `company` field is filled. Phase 5
  tailoring should use the `company` field rather than rely on the text.
