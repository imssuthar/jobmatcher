package profile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// minTextChars is the least text a real resume yields; less usually means a
// scanned PDF (no OCR yet) or an empty file.
const minTextChars = 200

// ObjectStore keeps the original files.
type ObjectStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) ([]byte, error)
}

// TextExtractor converts documents to plain text.
type TextExtractor interface {
	ExtractText(ctx context.Context, data []byte, contentType string) (string, error)
}

// Service runs the resume pipeline and serves profile reads and fact edits.
type Service struct {
	Repo      Repo
	Objects   ObjectStore
	Text      TextExtractor
	Extractor *Extractor
	LLM       LLM
	Log       *slog.Logger

	EmbedModel  string
	DocPrefix   string
	QueryPrefix string
	Workers     int

	queue chan uuid.UUID
	wg    sync.WaitGroup
}

// Start launches the pipeline workers and re-queues resumes left unfinished
// by a previous run (at-least-once processing). Workers stop when ctx ends.
func (s *Service) Start(ctx context.Context) error {
	s.queue = make(chan uuid.UUID, 1024)
	for i := 0; i < max(s.Workers, 1); i++ {
		s.wg.Add(1)
		go s.worker(ctx)
	}
	ids, err := s.Repo.ListUnfinished(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		s.Log.Info("resuming unfinished resume", "resume_id", id)
		s.queue <- id
	}
	return nil
}

// Wait blocks until all workers have exited.
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) worker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.queue:
			s.process(ctx, id)
		}
	}
}

// Submit validates and stores an upload and queues it for processing. When the
// same file was already uploaded it returns the existing resume and dedup=true.
func (s *Service) Submit(ctx context.Context, filename string, data []byte) (*Resume, bool, error) {
	contentType, ext, err := DetectType(filename, data)
	if err != nil {
		return nil, false, err
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	if existing, err := s.Repo.FindLiveResumeBySHA(ctx, sha); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	r := &Resume{
		ID:          uuid.New(),
		SHA256:      sha,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		ObjectKey:   "resumes/" + sha + ext,
		Status:      StatusQueued,
	}
	if err := s.Objects.Put(ctx, r.ObjectKey, data, contentType); err != nil {
		return nil, false, fmt.Errorf("store original: %w", err)
	}
	if err := s.Repo.CreateResume(ctx, r); errors.Is(err, ErrDuplicate) {
		// Lost a race with a concurrent upload of the same file.
		existing, err := s.Repo.FindLiveResumeBySHA(ctx, sha)
		return existing, true, err
	} else if err != nil {
		return nil, false, err
	}
	s.queue <- r.ID
	return r, false, nil
}

// process runs every pipeline stage for one resume. It always starts from the
// beginning, so a crash mid-way is recovered by simply processing again.
func (s *Service) process(ctx context.Context, id uuid.UUID) {
	ctx, span := otel.Tracer("jobmatcher/profile").Start(ctx, "resume.process")
	defer span.End()
	span.SetAttributes(attribute.String("resume.id", id.String()), attribute.String("openinference.span.kind", "CHAIN"))
	log := s.Log.With("resume_id", id)

	stage := StatusParsing
	err := s.run(ctx, id, func(st string) error {
		stage = st
		log.Info("stage", "status", st)
		return s.Repo.SetStatus(ctx, id, st)
	})
	switch {
	case err == nil:
		log.Info("resume ready")
	case ctx.Err() != nil:
		// Shutting down: leave the status as is so the next start resumes it.
		log.Warn("interrupted by shutdown", "stage", stage)
	default:
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		log.Error("resume failed", "stage", stage, "error", err)
		if mErr := s.Repo.MarkFailed(context.WithoutCancel(ctx), id, stage, err.Error()); mErr != nil {
			log.Error("mark failed", "error", mErr)
		}
	}
}

func (s *Service) run(ctx context.Context, id uuid.UUID, advance func(string) error) error {
	r, err := s.Repo.GetResume(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Repo.DiscardDraft(ctx, id); err != nil {
		return err
	}

	if err := advance(StatusParsing); err != nil {
		return err
	}
	data, err := s.Objects.Get(ctx, r.ObjectKey)
	if err != nil {
		return fmt.Errorf("load original: %w", err)
	}
	text, err := s.Text.ExtractText(ctx, data, r.ContentType)
	if err != nil {
		return fmt.Errorf("extract text: %w", err)
	}
	text = strings.TrimSpace(text)
	if len(text) < minTextChars {
		return fmt.Errorf("extract text: only %d characters found; the file may be empty, corrupt or a scanned image (OCR is not enabled)", len(text))
	}
	if err := s.Repo.SaveRawText(ctx, id, text); err != nil {
		return err
	}

	if err := advance(StatusExtractingProfile); err != nil {
		return err
	}
	pd, _, err := s.Extractor.ExtractProfile(ctx, text)
	if err != nil {
		return err
	}
	p := &Profile{ID: uuid.New(), ResumeID: id, Model: s.Extractor.ProfileModel, Data: pd}
	if err := s.Repo.SaveDraftProfile(ctx, p); err != nil {
		return err
	}

	if err := advance(StatusExtractingFacts); err != nil {
		return err
	}
	fd, _, err := s.Extractor.ExtractFacts(ctx, text, p.Data)
	if err != nil {
		return err
	}

	if err := advance(StatusEmbedding); err != nil {
		return err
	}
	texts := make([]string, len(fd.Facts))
	for i, f := range fd.Facts {
		texts[i] = f.Text
	}
	vecs, err := s.embed(ctx, s.DocPrefix, texts)
	if err != nil {
		return err
	}
	facts := make([]Fact, len(fd.Facts))
	for i, f := range fd.Facts {
		facts[i] = Fact{
			ID: uuid.New(), ProfileID: p.ID, Ordinal: i,
			Text: f.Text, Category: f.Category, Skills: f.Skills, Company: f.Company, Metric: f.Metric,
			Embedding: vecs[i],
		}
	}
	if err := s.Repo.ActivateProfile(ctx, p.ID, facts); err != nil {
		return err
	}
	return advance(StatusReady)
}

// embed calls the embedding model in batches. nomic-embed-text expects a task
// prefix ("search_document: " / "search_query: ") for best retrieval quality.
func (s *Service) embed(ctx context.Context, prefix string, texts []string) ([][]float32, error) {
	const batch = 32
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batch {
		end := min(start+batch, len(texts))
		in := make([]string, 0, end-start)
		for _, t := range texts[start:end] {
			in = append(in, prefix+t)
		}
		vecs, err := s.LLM.Embed(ctx, s.EmbedModel, in)
		if err != nil {
			return nil, fmt.Errorf("embed: %w", err)
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// UpdateFact changes a fact's text and re-embeds it.
func (s *Service) UpdateFact(ctx context.Context, id uuid.UUID, text string) (*Fact, error) {
	text = clean(text)
	if n := len(text); n < 15 || n > 500 {
		return nil, &ValidationError{Problems: []string{"text must be 15-500 characters"}}
	}
	vecs, err := s.embed(ctx, s.DocPrefix, []string{text})
	if err != nil {
		return nil, err
	}
	if err := s.Repo.UpdateFact(ctx, id, text, vecs[0]); err != nil {
		return nil, err
	}
	return s.Repo.GetFact(ctx, id)
}

// Search finds the facts most similar to a query within a profile.
func (s *Service) Search(ctx context.Context, profileID uuid.UUID, query string, k int) ([]ScoredFact, error) {
	vecs, err := s.embed(ctx, s.QueryPrefix, []string{query})
	if err != nil {
		return nil, err
	}
	return s.Repo.SearchFacts(ctx, profileID, vecs[0], k)
}
