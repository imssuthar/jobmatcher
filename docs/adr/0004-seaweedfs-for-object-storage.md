# ADR 0004: SeaweedFS for local S3-compatible storage

**Status:** accepted

## Context

Original resume files are kept in object storage so the pipeline can be re-run from the
source file. The design called for MinIO, but MinIO stopped publishing community Docker
images: `minio/minio` is no longer pullable from Docker Hub and the Quay copy requires auth.
A fresh clone must work with `make up` and nothing else.

## Decision

Run **SeaweedFS** (`chrislusf/seaweedfs`) with its S3 gateway enabled. The Go service keeps
using the MinIO Go client, which speaks the plain S3 API, so the code depends only on "S3",
configured through `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` and `S3_BUCKET`.

## Consequences

- Any S3-compatible store (AWS S3, Garage, RustFS, Ceph) can replace it with an env change.
- SeaweedFS runs master, volume, filer and S3 in one container; the filer UI on port 8888
  lets you browse stored files.
- Credentials are rendered from `.env` into `.run/s3.json` by `scripts/render.sh`.
