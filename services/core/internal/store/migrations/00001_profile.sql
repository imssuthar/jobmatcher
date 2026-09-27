-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

-- One row per uploaded file. status walks the pipeline:
-- queued -> parsing -> extracting_profile -> extracting_facts -> embedding -> ready | failed
CREATE TABLE resumes (
    id            uuid PRIMARY KEY,
    sha256        text        NOT NULL,
    filename      text        NOT NULL,
    content_type  text        NOT NULL,
    size_bytes    bigint      NOT NULL,
    object_key    text        NOT NULL,
    status        text        NOT NULL,
    failed_stage  text        NOT NULL DEFAULT '',
    error         text        NOT NULL DEFAULT '',
    raw_text      text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- Same file is processed once; a failed upload may be retried.
CREATE UNIQUE INDEX resumes_sha256_live ON resumes (sha256) WHERE status <> 'failed';

-- Structured profile extracted from a resume. Each upload creates a new
-- version; exactly one version is active (single-user app).
CREATE TABLE profiles (
    id          uuid PRIMARY KEY,
    resume_id   uuid        NOT NULL UNIQUE REFERENCES resumes (id) ON DELETE CASCADE,
    version     int         NOT NULL,
    model       text        NOT NULL,
    data        jsonb       NOT NULL,
    is_active   boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX profiles_single_active ON profiles ((true)) WHERE is_active;

-- The fact bank: atomic, verifiable statements used later for matching and tailoring.
CREATE TABLE profile_facts (
    id          uuid PRIMARY KEY,
    profile_id  uuid        NOT NULL REFERENCES profiles (id) ON DELETE CASCADE,
    ordinal     int         NOT NULL,
    text        text        NOT NULL,
    category    text        NOT NULL,
    skills      text[]      NOT NULL DEFAULT '{}',
    company     text        NOT NULL DEFAULT '',
    metric      text        NOT NULL DEFAULT '',
    embedding   vector(768),
    edited      boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX profile_facts_profile ON profile_facts (profile_id, ordinal);
CREATE INDEX profile_facts_embedding ON profile_facts USING hnsw (embedding vector_cosine_ops);

-- +goose Down
DROP TABLE profile_facts;
DROP TABLE profiles;
DROP TABLE resumes;
