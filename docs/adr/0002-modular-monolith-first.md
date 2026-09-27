# ADR 0002: Start with a modular monolith

**Status:** accepted

## Context

The target design has separate profile, job, match, tracker, tailor and agent services.
Splitting them on day one adds deployment, networking and contract overhead before the
boundaries have been proven.

## Decision

Phases 1–3 build profile, jobs and match as **packages inside one Go service** (`core`),
each owning its tables and exposing a narrow interface (`profile.Repo`, `profile.Service`).
Services are split out in Phase 4, when Temporal workflows make the boundaries explicit.

## Consequences

- One binary, one migration set, simple local debugging.
- Package boundaries must be respected by convention (no cross-package table access)
  so the later split is mechanical.
