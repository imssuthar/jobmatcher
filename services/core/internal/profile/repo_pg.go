package profile

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// PGRepo implements Repo on Postgres + pgvector.
type PGRepo struct{ db *pgxpool.Pool }

// NewPGRepo wraps a connection pool.
func NewPGRepo(db *pgxpool.Pool) *PGRepo { return &PGRepo{db: db} }

func (r *PGRepo) Ping(ctx context.Context) error { return r.db.Ping(ctx) }

const resumeCols = `id, sha256, filename, content_type, size_bytes, object_key, status, failed_stage, error, created_at, updated_at`

func scanResume(row pgx.Row) (*Resume, error) {
	var x Resume
	err := row.Scan(&x.ID, &x.SHA256, &x.Filename, &x.ContentType, &x.SizeBytes, &x.ObjectKey,
		&x.Status, &x.FailedStage, &x.Error, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &x, err
}

func (r *PGRepo) CreateResume(ctx context.Context, x *Resume) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO resumes (id, sha256, filename, content_type, size_bytes, object_key, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at`,
		x.ID, x.SHA256, x.Filename, x.ContentType, x.SizeBytes, x.ObjectKey, x.Status,
	).Scan(&x.CreatedAt, &x.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDuplicate
	}
	return err
}

func (r *PGRepo) GetResume(ctx context.Context, id uuid.UUID) (*Resume, error) {
	return scanResume(r.db.QueryRow(ctx, `SELECT `+resumeCols+` FROM resumes WHERE id = $1`, id))
}

func (r *PGRepo) FindLiveResumeBySHA(ctx context.Context, sha string) (*Resume, error) {
	return scanResume(r.db.QueryRow(ctx,
		`SELECT `+resumeCols+` FROM resumes WHERE sha256 = $1 AND status <> 'failed'`, sha))
}

func (r *PGRepo) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := r.db.Exec(ctx, `UPDATE resumes SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	return err
}

func (r *PGRepo) MarkFailed(ctx context.Context, id uuid.UUID, stage, msg string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE resumes SET status = 'failed', failed_stage = $2, error = $3, updated_at = now()
		WHERE id = $1`, id, stage, msg)
	return err
}

func (r *PGRepo) SaveRawText(ctx context.Context, id uuid.UUID, text string) error {
	_, err := r.db.Exec(ctx, `UPDATE resumes SET raw_text = $2, updated_at = now() WHERE id = $1`, id, text)
	return err
}

func (r *PGRepo) ListUnfinished(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id FROM resumes WHERE status NOT IN ('ready', 'failed') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (r *PGRepo) DiscardDraft(ctx context.Context, resumeID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM profiles WHERE resume_id = $1 AND NOT is_active`, resumeID)
	return err
}

func (r *PGRepo) SaveDraftProfile(ctx context.Context, p *Profile) error {
	data, err := json.Marshal(p.Data)
	if err != nil {
		return err
	}
	return r.db.QueryRow(ctx, `
		INSERT INTO profiles (id, resume_id, version, model, data)
		VALUES ($1, $2, (SELECT coalesce(max(version), 0) + 1 FROM profiles), $3, $4)
		RETURNING version, created_at`,
		p.ID, p.ResumeID, p.Model, data,
	).Scan(&p.Version, &p.CreatedAt)
}

func (r *PGRepo) ActivateProfile(ctx context.Context, profileID uuid.UUID, facts []Fact) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		for _, f := range facts {
			if _, err := tx.Exec(ctx, `
				INSERT INTO profile_facts (id, profile_id, ordinal, text, category, skills, company, metric, embedding)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				f.ID, profileID, f.Ordinal, f.Text, f.Category, f.Skills, f.Company, f.Metric,
				pgvector.NewVector(f.Embedding)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE profiles SET is_active = false WHERE is_active`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE profiles SET is_active = true WHERE id = $1`, profileID)
		return err
	})
}

const profileCols = `id, resume_id, version, model, is_active, created_at, data`

func scanProfile(row pgx.Row) (*Profile, error) {
	var p Profile
	var data []byte
	err := row.Scan(&p.ID, &p.ResumeID, &p.Version, &p.Model, &p.IsActive, &p.CreatedAt, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, json.Unmarshal(data, &p.Data)
}

func (r *PGRepo) ActiveProfile(ctx context.Context) (*Profile, error) {
	return scanProfile(r.db.QueryRow(ctx, `SELECT `+profileCols+` FROM profiles WHERE is_active`))
}

func (r *PGRepo) GetProfile(ctx context.Context, id uuid.UUID) (*Profile, error) {
	return scanProfile(r.db.QueryRow(ctx, `SELECT `+profileCols+` FROM profiles WHERE id = $1`, id))
}

func (r *PGRepo) ProfileByResume(ctx context.Context, resumeID uuid.UUID) (*Profile, error) {
	return scanProfile(r.db.QueryRow(ctx, `SELECT `+profileCols+` FROM profiles WHERE resume_id = $1`, resumeID))
}

func (r *PGRepo) CountProfiles(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM profiles`).Scan(&n)
	return n, err
}

const factCols = `id, profile_id, ordinal, text, category, skills, company, metric, edited, coalesce(vector_dims(embedding), 0)`

func scanFact(row pgx.Row) (Fact, error) {
	var f Fact
	err := row.Scan(&f.ID, &f.ProfileID, &f.Ordinal, &f.Text, &f.Category, &f.Skills, &f.Company, &f.Metric, &f.Edited, &f.EmbeddingDim)
	return f, err
}

func (r *PGRepo) ListFacts(ctx context.Context, profileID uuid.UUID) ([]Fact, error) {
	rows, err := r.db.Query(ctx, `SELECT `+factCols+` FROM profile_facts WHERE profile_id = $1 ORDER BY ordinal`, profileID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Fact, error) { return scanFact(row) })
}

func (r *PGRepo) GetFact(ctx context.Context, id uuid.UUID) (*Fact, error) {
	f, err := scanFact(r.db.QueryRow(ctx, `SELECT `+factCols+` FROM profile_facts WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &f, err
}

func (r *PGRepo) UpdateFact(ctx context.Context, id uuid.UUID, text string, embedding []float32) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE profile_facts SET text = $2, embedding = $3, edited = true, updated_at = now()
		WHERE id = $1`, id, text, pgvector.NewVector(embedding))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (r *PGRepo) DeleteFact(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM profile_facts WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (r *PGRepo) SearchFacts(ctx context.Context, profileID uuid.UUID, embedding []float32, k int) ([]ScoredFact, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+factCols+`, 1 - (embedding <=> $2) AS score
		FROM profile_facts
		WHERE profile_id = $1 AND embedding IS NOT NULL
		ORDER BY embedding <=> $2
		LIMIT $3`, profileID, pgvector.NewVector(embedding), k)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ScoredFact, error) {
		var s ScoredFact
		err := row.Scan(&s.ID, &s.ProfileID, &s.Ordinal, &s.Text, &s.Category, &s.Skills, &s.Company, &s.Metric, &s.Edited, &s.EmbeddingDim, &s.Score)
		return s, err
	})
}
