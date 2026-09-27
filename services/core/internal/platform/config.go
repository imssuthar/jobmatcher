// Package platform holds cross-cutting concerns: configuration, logging and telemetry.
package platform

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the full runtime configuration of the core service. Every field
// comes from an environment variable so the same image runs in compose, in
// tests and (later) in Kubernetes.
type Config struct {
	HTTPAddr       string
	DatabaseURL    string
	MaxUploadBytes int64
	Workers        int

	// S3-compatible object storage (SeaweedFS locally).
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool

	TikaURL string

	LLMBaseURL string
	LLMAPIKey  string
	LLMTimeout time.Duration

	// Model aliases as configured in the LiteLLM gateway.
	ModelProfile string
	ModelFacts   string
	ModelEmbed   string

	EmbedDim         int
	EmbedDocPrefix   string
	EmbedQueryPrefix string

	OTLPEndpoint string
	ServiceName  string
}

// LoadConfig reads the configuration from the environment and applies defaults.
func LoadConfig() (Config, error) {
	c := Config{
		HTTPAddr:       env("HTTP_ADDR", ":8080"),
		DatabaseURL:    env("DATABASE_URL", "postgres://jobmatcher:jobmatcher@localhost:5432/jobmatcher?sslmode=disable"),
		MaxUploadBytes: int64(envInt("MAX_UPLOAD_MB", 10)) << 20,
		Workers:        envInt("PIPELINE_WORKERS", 1),

		S3Endpoint:  env("S3_ENDPOINT", "localhost:8333"),
		S3AccessKey: env("S3_ACCESS_KEY", "jobmatcher"),
		S3SecretKey: env("S3_SECRET_KEY", "jobmatcher-secret"),
		S3Bucket:    env("S3_BUCKET", "resumes"),
		S3UseSSL:    env("S3_USE_SSL", "false") == "true",

		TikaURL: env("TIKA_URL", "http://localhost:9998"),

		LLMBaseURL: env("LLM_BASE_URL", "http://localhost:4000"),
		LLMAPIKey:  env("LLM_API_KEY", "sk-jobmatcher-local"),
		LLMTimeout: time.Duration(envInt("LLM_TIMEOUT_SECONDS", 300)) * time.Second,

		ModelProfile: env("MODEL_PROFILE", "smart"),
		ModelFacts:   env("MODEL_FACTS", "smart"),
		ModelEmbed:   env("MODEL_EMBED", "embed"),

		EmbedDim:         envInt("EMBED_DIM", 768),
		EmbedDocPrefix:   env("EMBED_DOC_PREFIX", "search_document: "),
		EmbedQueryPrefix: env("EMBED_QUERY_PREFIX", "search_query: "),

		OTLPEndpoint: env("OTLP_ENDPOINT", ""),
		ServiceName:  env("SERVICE_NAME", "jobmatcher-core"),
	}
	// The vector column is created with a fixed dimension by the migration.
	if c.EmbedDim != 768 {
		return c, fmt.Errorf("EMBED_DIM=%d but the schema uses vector(768); add a migration before changing embedding models", c.EmbedDim)
	}
	if c.Workers < 1 {
		c.Workers = 1
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v, err := strconv.Atoi(env(key, ""))
	if err != nil {
		return def
	}
	return v
}
