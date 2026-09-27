package profile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const sampleResume = `Jane Doe
jane.doe@example.com | Pune, India

Senior Software Engineer with 6 years building distributed systems in Go and Java.

Experience
Acme Corp - Senior Software Engineer (2021-03 to present)
- Built a Go job scheduler with at-least-once delivery handling 2M jobs per day.
- Migrated 40 services from EC2 to Kubernetes with zero downtime.

Globex - Software Engineer (2019-01 to 2021-02)
- Designed event-driven microservices on Kafka processing 15,000 events per second.

Skills: Go, Java, Kafka, Kubernetes, PostgreSQL`

func validProfileJSON() string {
	return `{"name":"Jane Doe","email":"jane.doe@example.com","phone":"","location":"Pune, India",
	"headline":"Senior Software Engineer","summary":"Distributed systems engineer.","total_years_experience":6,
	"skills":["Go","Java","kafka","Kafka","Kubernetes","PostgreSQL"],"links":[],
	"experience":[{"company":"Acme Corp","title":"Senior Software Engineer","location":"","start":"2021-03","end":"present","highlights":["Built a Go job scheduler"]},
	{"company":"Globex","title":"Software Engineer","location":"","start":"2019-01","end":"2021-02","highlights":[]}],
	"education":[]}`
}

func validFactsJSON() string {
	return `{"facts":[
	{"text":"Built a Go job scheduler with at-least-once delivery handling 2M jobs per day at Acme Corp.","category":"achievement","skills":["Go"],"company":"Acme Corp","metric":"2M jobs per day"},
	{"text":"Migrated 40 services from EC2 to Kubernetes with zero downtime at Acme Corp.","category":"achievement","skills":["Kubernetes"],"company":"Acme Corp","metric":"40 services"},
	{"text":"Designed event-driven microservices on Kafka processing 15,000 events per second at Globex.","category":"achievement","skills":["Kafka"],"company":"Globex","metric":"15,000 events per second"},
	{"text":"Designed event-driven microservices on Kafka processing 15,000 events per second at Globex.","category":"achievement","skills":["Kafka"],"company":"Globex","metric":""}
	]}`
}

// --- validation --------------------------------------------------------------

func TestProfileNormalizeDedupesSkills(t *testing.T) {
	var p ProfileData
	if err := json.Unmarshal([]byte(validProfileJSON()), &p); err != nil {
		t.Fatal(err)
	}
	p.Normalize()
	if got := strings.Join(p.Skills, ","); got != "Go,Java,kafka,Kubernetes,PostgreSQL" {
		t.Fatalf("skills = %s", got)
	}
	if err := p.Validate(sampleResume); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}
}

func TestProfileValidateRejectsHallucinations(t *testing.T) {
	cases := map[string]func(*ProfileData){
		"missing name":      func(p *ProfileData) { p.Name = "" },
		"name not in text":  func(p *ProfileData) { p.Name = "John Smith" },
		"email not in text": func(p *ProfileData) { p.Email = "someone@else.com" },
		"invalid email":     func(p *ProfileData) { p.Email = "not-an-email" },
		"no skills":         func(p *ProfileData) { p.Skills = nil },
		"negative years":    func(p *ProfileData) { p.TotalYearsExperience = -1 },
		"role without title": func(p *ProfileData) {
			p.Experience = []Experience{{Company: "Acme Corp"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var p ProfileData
			_ = json.Unmarshal([]byte(validProfileJSON()), &p)
			p.Normalize()
			mutate(&p)
			var ve *ValidationError
			if err := p.Validate(sampleResume); !errors.As(err, &ve) {
				t.Fatalf("expected ValidationError, got %v", err)
			}
		})
	}
}

func TestFactsNormalizeAndValidate(t *testing.T) {
	var f FactsData
	_ = json.Unmarshal([]byte(validFactsJSON()), &f)
	f.Normalize()
	if len(f.Facts) != 3 {
		t.Fatalf("duplicate fact not removed: %d facts", len(f.Facts))
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}

	f.Facts = append(f.Facts, FactItem{Text: "short", Category: "bogus"})
	var ve *ValidationError
	if err := f.Validate(); !errors.As(err, &ve) || len(ve.Problems) != 2 {
		t.Fatalf("expected 2 problems, got %v", err)
	}
}

func TestNumberGrounding(t *testing.T) {
	g := NumberGrounding(sampleResume, []string{
		"Handled 2M jobs per day",
		"Processed 15,000 events per second",
		"Cut latency by 37%", // invented
	})
	if g.NumbersTotal != 3 || g.NumbersGrounded != 2 || g.Ungrounded[0] != "37" {
		t.Fatalf("unexpected grounding %+v", g)
	}
	if NumberGrounding("x", nil).Rate() != 1 {
		t.Fatal("rate with no numbers should be 1")
	}
}

func TestDetectType(t *testing.T) {
	cases := []struct {
		name, file string
		data       string
		want       string
		wantErr    bool
	}{
		{"pdf", "cv.PDF", "%PDF-1.7 ...", "application/pdf", false},
		{"docx", "cv.docx", "PK\x03\x04rest", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", false},
		{"zip named docx only", "cv.zip", "PK\x03\x04rest", "", true},
		{"txt", "cv.txt", "hello", "text/plain; charset=utf-8", false},
		{"binary txt", "cv.txt", "a\x00b", "", true},
		{"png", "cv.png", "\x89PNG", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := DetectType(c.file, []byte(c.data))
			if (err != nil) != c.wantErr || got != c.want {
				t.Fatalf("got %q err=%v", got, err)
			}
		})
	}
}

func TestDecodeToleratesCodeFence(t *testing.T) {
	var v struct{ A int }
	if err := decode("```json\n{\"A\": 1}\n```", &v); err != nil || v.A != 1 {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}

// --- extractor ---------------------------------------------------------------

func TestExtractorRetriesWithFeedback(t *testing.T) {
	fake := newFakeLLM()
	fake.on("profile", `not json`, validProfileJSON())
	x := &Extractor{LLM: fake, ProfileModel: "smart", MaxRetries: 2}

	p, u, err := x.ExtractProfile(context.Background(), sampleResume)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Jane Doe" || u.Attempts != 2 {
		t.Fatalf("name=%q attempts=%d", p.Name, u.Attempts)
	}
	second := fake.requests[1].Messages
	last := second[len(second)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "not valid JSON") {
		t.Fatalf("retry did not include the validation error: %+v", last)
	}
	if fake.requests[0].Temperature == nil || *fake.requests[0].Temperature != 0 {
		t.Fatal("extraction must be deterministic")
	}
}

func TestExtractorGivesUpAfterMaxRetries(t *testing.T) {
	fake := newFakeLLM()
	fake.on("profile", `{"name":"Nobody"}`)
	x := &Extractor{LLM: fake, ProfileModel: "smart", MaxRetries: 2}

	_, u, err := x.ExtractProfile(context.Background(), sampleResume)
	var ve *ValidationError
	if !errors.As(err, &ve) || u.Attempts != 3 {
		t.Fatalf("attempts=%d err=%v", u.Attempts, err)
	}
}

// --- service -----------------------------------------------------------------

func newTestService(t *testing.T, fake *fakeLLM, text TextExtractor) (*Service, *memRepo, context.CancelFunc) {
	t.Helper()
	repo := newMemRepo()
	svc := &Service{
		Repo:        repo,
		Objects:     &memObjects{},
		Text:        text,
		LLM:         fake,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Extractor:   &Extractor{LLM: fake, ProfileModel: "smart", FactsModel: "smart", MaxRetries: 1},
		EmbedModel:  "embed",
		DocPrefix:   "search_document: ",
		QueryPrefix: "search_query: ",
		Workers:     1,
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); svc.Wait() })
	return svc, repo, cancel
}

func waitTerminal(t *testing.T, repo *memRepo, id uuid.UUID) *Resume {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := repo.GetResume(context.Background(), id)
		if r.Status == StatusReady || r.Status == StatusFailed {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("resume did not finish")
	return nil
}

func TestPipelineHappyPath(t *testing.T) {
	fake := newFakeLLM()
	fake.on("profile", validProfileJSON())
	fake.on("facts", validFactsJSON())
	svc, repo, _ := newTestService(t, fake, plainText{})
	ctx := context.Background()

	res, dedup, err := svc.Submit(ctx, "jane.txt", []byte(sampleResume))
	if err != nil || dedup {
		t.Fatalf("submit: dedup=%v err=%v", dedup, err)
	}
	if r := waitTerminal(t, repo, res.ID); r.Status != StatusReady {
		t.Fatalf("status=%s error=%s", r.Status, r.Error)
	}
	want := []string{StatusParsing, StatusExtractingProfile, StatusExtractingFacts, StatusEmbedding, StatusReady}
	if got := strings.Join(repo.statuses[res.ID], ","); got != strings.Join(want, ",") {
		t.Fatalf("stages = %s", got)
	}

	p, err := repo.ActiveProfile(ctx)
	if err != nil || p.Data.Name != "Jane Doe" {
		t.Fatalf("active profile: %+v %v", p, err)
	}
	facts, _ := repo.ListFacts(ctx, p.ID)
	if len(facts) != 3 {
		t.Fatalf("facts = %d", len(facts))
	}
	for _, f := range facts {
		if f.EmbeddingDim != 768 {
			t.Fatalf("fact %q has embedding dim %d", f.Text, f.EmbeddingDim)
		}
	}

	hits, err := svc.Search(ctx, p.ID, "kafka events per second", 1)
	if err != nil || !strings.Contains(hits[0].Text, "Kafka") {
		t.Fatalf("search: %+v %v", hits, err)
	}

	// Same bytes again: deduplicated, no new profile version.
	again, dedup, err := svc.Submit(ctx, "renamed.txt", []byte(sampleResume))
	if err != nil || !dedup || again.ID != res.ID {
		t.Fatalf("expected dedup to %s, got %v dedup=%v err=%v", res.ID, again, dedup, err)
	}
	if n, _ := repo.CountProfiles(ctx); n != 1 {
		t.Fatalf("profiles = %d", n)
	}
}

func TestPipelineFailsOnEmptyText(t *testing.T) {
	fake := newFakeLLM()
	svc, repo, _ := newTestService(t, fake, plainText{})

	res, _, err := svc.Submit(context.Background(), "empty.txt", []byte("too short"))
	if err != nil {
		t.Fatal(err)
	}
	r := waitTerminal(t, repo, res.ID)
	if r.Status != StatusFailed || r.FailedStage != StatusParsing || !strings.Contains(r.Error, "characters") {
		t.Fatalf("got %+v", r)
	}
	if len(fake.requests) != 0 {
		t.Fatal("LLM must not be called when there is no text")
	}
}

func TestPipelineFailsOnBadModelOutput(t *testing.T) {
	fake := newFakeLLM()
	fake.on("profile", validProfileJSON())
	fake.on("facts", `{"facts":[]}`)
	svc, repo, _ := newTestService(t, fake, plainText{})

	res, _, _ := svc.Submit(context.Background(), "jane.txt", []byte(sampleResume))
	r := waitTerminal(t, repo, res.ID)
	if r.Status != StatusFailed || r.FailedStage != StatusExtractingFacts {
		t.Fatalf("got %+v", r)
	}
	if _, err := repo.ActiveProfile(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatal("a failed resume must not activate a profile")
	}
}

func TestFailedUploadCanBeRetried(t *testing.T) {
	fake := newFakeLLM()
	svc, repo, _ := newTestService(t, fake, plainText{err: errors.New("tika down")})

	first, _, _ := svc.Submit(context.Background(), "jane.txt", []byte(sampleResume))
	waitTerminal(t, repo, first.ID)
	second, dedup, err := svc.Submit(context.Background(), "jane.txt", []byte(sampleResume))
	if err != nil || dedup || second.ID == first.ID {
		t.Fatalf("failed resume should be re-processable: dedup=%v err=%v", dedup, err)
	}
}

func TestUpdateFactReembeds(t *testing.T) {
	fake := newFakeLLM()
	fake.on("profile", validProfileJSON())
	fake.on("facts", validFactsJSON())
	svc, repo, _ := newTestService(t, fake, plainText{})
	ctx := context.Background()

	res, _, _ := svc.Submit(ctx, "jane.txt", []byte(sampleResume))
	waitTerminal(t, repo, res.ID)
	p, _ := repo.ActiveProfile(ctx)
	facts, _ := repo.ListFacts(ctx, p.ID)

	updated, err := svc.UpdateFact(ctx, facts[0].ID, "Wrote a lunar greenhouse irrigation controller in Rust")
	if err != nil || !updated.Edited {
		t.Fatalf("update: %+v %v", updated, err)
	}
	hits, _ := svc.Search(ctx, p.ID, "lunar greenhouse irrigation", 1)
	if hits[0].ID != facts[0].ID {
		t.Fatalf("edited fact not found by search: %+v", hits[0])
	}
	if _, err := svc.UpdateFact(ctx, facts[0].ID, "tiny"); err == nil {
		t.Fatal("too-short text must be rejected")
	}
}
