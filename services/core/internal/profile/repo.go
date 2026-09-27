package profile

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrDuplicate is returned by CreateResume when a live resume with the same
// content already exists.
var ErrDuplicate = errors.New("duplicate resume")

// Repo is the persistence the pipeline needs. The Postgres implementation is
// in repo_pg.go; tests use an in-memory one.
type Repo interface {
	Ping(ctx context.Context) error

	CreateResume(ctx context.Context, r *Resume) error
	GetResume(ctx context.Context, id uuid.UUID) (*Resume, error)
	FindLiveResumeBySHA(ctx context.Context, sha string) (*Resume, error)
	SetStatus(ctx context.Context, id uuid.UUID, status string) error
	MarkFailed(ctx context.Context, id uuid.UUID, stage, msg string) error
	SaveRawText(ctx context.Context, id uuid.UUID, text string) error
	ListUnfinished(ctx context.Context) ([]uuid.UUID, error)
	// DiscardDraft removes a partially built profile so a resume can be
	// reprocessed from the start after a crash.
	DiscardDraft(ctx context.Context, resumeID uuid.UUID) error

	SaveDraftProfile(ctx context.Context, p *Profile) error
	// ActivateProfile stores the facts and makes the profile the active one,
	// in a single transaction.
	ActivateProfile(ctx context.Context, profileID uuid.UUID, facts []Fact) error
	ActiveProfile(ctx context.Context) (*Profile, error)
	GetProfile(ctx context.Context, id uuid.UUID) (*Profile, error)
	ProfileByResume(ctx context.Context, resumeID uuid.UUID) (*Profile, error)
	CountProfiles(ctx context.Context) (int, error)

	ListFacts(ctx context.Context, profileID uuid.UUID) ([]Fact, error)
	GetFact(ctx context.Context, id uuid.UUID) (*Fact, error)
	UpdateFact(ctx context.Context, id uuid.UUID, text string, embedding []float32) error
	DeleteFact(ctx context.Context, id uuid.UUID) error
	SearchFacts(ctx context.Context, profileID uuid.UUID, embedding []float32, k int) ([]ScoredFact, error)
}
