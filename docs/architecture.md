# Architecture (Phase 1)

JobMatcher runs entirely on your machine. Everything except the model runtime runs in
Docker; on macOS Ollama runs natively so models can use the Apple GPU (Metal).

## System overview

```mermaid
flowchart LR
    user(["You"])

    subgraph clients["Clients"]
        ui["Web UI<br/>localhost:8080"]
        cli["jmctl CLI<br/>make demo"]
    end

    subgraph docker["Docker Compose stack"]
        core["core service (Go)<br/>HTTP API + pipeline workers"]
        tika["Apache Tika<br/>PDF / DOCX → text"]
        s3["SeaweedFS (S3)<br/>original files"]
        pg[("Postgres 17 + pgvector<br/>profiles · facts · embeddings")]
        litellm["LiteLLM gateway<br/>aliases: smart · fast · embed"]
        phoenix["Phoenix<br/>LLM traces"]
    end

    subgraph host["Host (macOS)"]
        ollama["Ollama (Metal GPU)<br/>qwen2.5:7b · qwen2.5:3b<br/>nomic-embed-text"]
    end

    user --> ui
    user --> cli
    ui -->|REST| core
    cli -->|REST| core
    core -->|extract text| tika
    core -->|store / load file| s3
    core -->|SQL + vector search| pg
    core -->|chat + embeddings| litellm
    litellm -->|OpenAI-compatible calls| ollama
    core -.->|OpenTelemetry spans| phoenix
```

Services only ever call **model aliases** on the gateway (`smart`, `fast`, `embed`), never a
concrete model or Ollama directly. Swapping a model is a change to `.env`.

## Resume pipeline

An upload returns immediately (`202 Accepted`); a worker processes it in the background and
the client polls the status.

```mermaid
sequenceDiagram
    autonumber
    participant C as Client (UI / jmctl)
    participant API as core API
    participant W as Pipeline worker
    participant S3 as SeaweedFS
    participant T as Tika
    participant L as LiteLLM → Ollama
    participant DB as Postgres + pgvector

    C->>API: POST /v1/resumes (file)
    API->>API: detect type, sha256
    alt same file already processed
        API-->>C: 200 existing resume (deduplicated)
    else new file
        API->>S3: store original
        API->>DB: insert resume (queued)
        API-->>C: 202 {id}
        API->>W: enqueue id
    end

    W->>S3: load original
    W->>T: extract text
    W->>DB: status = extracting_profile
    loop until valid (max 2 retries)
        W->>L: smart + JSON schema → profile
        W->>W: validate + grounding check<br/>(name/email must be in text)
    end
    W->>DB: save draft profile
    W->>DB: status = extracting_facts
    loop until valid (max 2 retries)
        W->>L: smart + JSON schema → facts
        W->>W: validate facts
    end
    W->>L: embed facts (search_document: prefix)
    W->>DB: one transaction: insert facts + activate profile
    W->>DB: status = ready

    C->>API: GET /v1/resumes/{id} (poll)
    API-->>C: ready + profile_id
```

## Resume states

```mermaid
stateDiagram-v2
    [*] --> queued: upload accepted
    queued --> parsing
    parsing --> extracting_profile: text extracted
    extracting_profile --> extracting_facts: profile valid
    extracting_facts --> embedding: facts valid
    embedding --> ready: profile activated
    parsing --> failed: unreadable / no text
    extracting_profile --> failed: invalid after retries
    extracting_facts --> failed: invalid after retries
    embedding --> failed: embedding error
    ready --> [*]
    failed --> [*]: same file may be re-uploaded
```

On restart, any resume not in `ready` or `failed` is re-queued and processed from the start
(at-least-once). A shutdown never marks work as failed.

## Data model

```mermaid
erDiagram
    RESUMES ||--o| PROFILES : "produces"
    PROFILES ||--o{ PROFILE_FACTS : "has"

    RESUMES {
        uuid id PK
        text sha256 "unique while not failed"
        text filename
        text content_type
        text object_key "S3 key"
        text status
        text failed_stage
        text error
        text raw_text
    }
    PROFILES {
        uuid id PK
        uuid resume_id FK
        int version
        text model
        jsonb data "name, email, skills, experience, education"
        bool is_active "exactly one active"
    }
    PROFILE_FACTS {
        uuid id PK
        uuid profile_id FK
        int ordinal
        text text
        text category
        text_array skills
        text company
        text metric
        vector_768 embedding "HNSW cosine index"
        bool edited
    }
```

## How quality is enforced

```mermaid
flowchart TB
    llm["LLM output"] --> schema{"Matches JSON schema?"}
    schema -- no --> retry["Send errors back to the model<br/>(max 2 retries)"]
    schema -- yes --> validate{"Go validation<br/>required fields, formats, categories"}
    validate -- no --> retry
    validate -- yes --> ground{"Grounded in resume?<br/>name + email appear in text"}
    ground -- no --> retry
    ground -- yes --> accept["Accepted"]
    retry --> llm
    retry -. "retries exhausted" .-> fail["Resume marked failed<br/>active profile unchanged"]
```

Temperature 0 and a fixed seed keep extraction reproducible. Acceptance tests then check
quality against golden fixtures with thresholds: skill recall ≥ 80%, company recall 100%,
≥ 90% of numbers in facts present in the resume, and search recall@3 ≥ 2/3.

## Run and verify

```mermaid
flowchart LR
    up["make up"] --> doctor["doctor<br/>docker, memory, disk, ports"]
    doctor --> ollama["start Ollama<br/>pull models"]
    ollama --> render["render configs<br/>.run/litellm.yaml, .run/s3.json"]
    render --> compose["docker compose up --build --wait"]
    compose --> warm["warm up models"]
    warm --> verify["make verify-phase1<br/>18 acceptance checks"]
```
