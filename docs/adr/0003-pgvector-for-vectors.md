# ADR 0003: Postgres + pgvector as the vector store

**Status:** accepted

## Context

The app needs relational data (profiles, jobs, applications), vector search (facts, jobs)
and, from Phase 3, keyword search for hybrid retrieval.

## Decision

Use one Postgres 17 instance with **pgvector** (HNSW, cosine distance) for embeddings and
Postgres full-text search for keywords. No separate vector database.

## Consequences

- One store to run, back up and migrate; vectors and rows update in the same transaction
  (profile activation is atomic).
- The vector dimension is fixed in the schema (`vector(768)`); changing embedding models
  requires a migration. The service refuses to start if `EMBED_DIM` disagrees.
- A pgvector vs Qdrant comparison is planned as a Phase 7 experiment.
