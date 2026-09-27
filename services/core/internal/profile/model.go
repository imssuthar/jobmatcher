// Package profile turns an uploaded resume into a structured profile and a
// fact bank: upload -> store -> parse -> extract profile -> extract facts ->
// embed -> activate.
package profile

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Resume pipeline states.
const (
	StatusQueued            = "queued"
	StatusParsing           = "parsing"
	StatusExtractingProfile = "extracting_profile"
	StatusExtractingFacts   = "extracting_facts"
	StatusEmbedding         = "embedding"
	StatusReady             = "ready"
	StatusFailed            = "failed"
)

// Fact categories the extractor may assign.
var FactCategories = []string{"achievement", "responsibility", "skill", "education", "certification", "other"}

// ErrNotFound is returned when an entity does not exist.
var ErrNotFound = errors.New("not found")

// Resume is an uploaded file and its pipeline state.
type Resume struct {
	ID          uuid.UUID `json:"id"`
	SHA256      string    `json:"sha256"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	ObjectKey   string    `json:"-"`
	Status      string    `json:"status"`
	FailedStage string    `json:"failed_stage,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Profile is one version of the structured profile.
type Profile struct {
	ID        uuid.UUID   `json:"id"`
	ResumeID  uuid.UUID   `json:"resume_id"`
	Version   int         `json:"version"`
	Model     string      `json:"model"`
	IsActive  bool        `json:"is_active"`
	CreatedAt time.Time   `json:"created_at"`
	Data      ProfileData `json:"data"`
}

// ProfileData is what the LLM extracts from the resume text.
type ProfileData struct {
	Name                 string       `json:"name"`
	Email                string       `json:"email"`
	Phone                string       `json:"phone"`
	Location             string       `json:"location"`
	Headline             string       `json:"headline"`
	Summary              string       `json:"summary"`
	TotalYearsExperience float64      `json:"total_years_experience"`
	Skills               []string     `json:"skills"`
	Experience           []Experience `json:"experience"`
	Education            []Education  `json:"education"`
	Links                []string     `json:"links"`
}

// Experience is one role.
type Experience struct {
	Company    string   `json:"company"`
	Title      string   `json:"title"`
	Location   string   `json:"location"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	Highlights []string `json:"highlights"`
}

// Education is one degree or course.
type Education struct {
	Institution string `json:"institution"`
	Degree      string `json:"degree"`
	Field       string `json:"field"`
	Year        string `json:"year"`
}

// FactsData is the LLM output for fact extraction.
type FactsData struct {
	Facts []FactItem `json:"facts"`
}

// FactItem is one extracted fact before it is stored.
type FactItem struct {
	Text     string   `json:"text"`
	Category string   `json:"category"`
	Skills   []string `json:"skills"`
	Company  string   `json:"company"`
	Metric   string   `json:"metric"`
}

// Fact is a stored fact-bank entry.
type Fact struct {
	ID           uuid.UUID `json:"id"`
	ProfileID    uuid.UUID `json:"profile_id"`
	Ordinal      int       `json:"ordinal"`
	Text         string    `json:"text"`
	Category     string    `json:"category"`
	Skills       []string  `json:"skills"`
	Company      string    `json:"company"`
	Metric       string    `json:"metric"`
	Edited       bool      `json:"edited"`
	EmbeddingDim int       `json:"embedding_dim"`
	Embedding    []float32 `json:"-"`
}

// ScoredFact is a search hit.
type ScoredFact struct {
	Fact
	Score float64 `json:"score"`
}
