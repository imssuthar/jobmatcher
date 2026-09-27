package profile

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/imssuthar/jobmatcher/services/core/internal/llm"
)

// memRepo is an in-memory Repo for unit tests.
type memRepo struct {
	mu       sync.Mutex
	resumes  map[uuid.UUID]*Resume
	rawText  map[uuid.UUID]string
	profiles map[uuid.UUID]*Profile
	facts    map[uuid.UUID]*Fact
	statuses map[uuid.UUID][]string
}

func newMemRepo() *memRepo {
	return &memRepo{
		resumes:  map[uuid.UUID]*Resume{},
		rawText:  map[uuid.UUID]string{},
		profiles: map[uuid.UUID]*Profile{},
		facts:    map[uuid.UUID]*Fact{},
		statuses: map[uuid.UUID][]string{},
	}
}

func (m *memRepo) Ping(context.Context) error { return nil }

func (m *memRepo) CreateResume(_ context.Context, r *Resume) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.resumes {
		if x.SHA256 == r.SHA256 && x.Status != StatusFailed {
			return ErrDuplicate
		}
	}
	c := *r
	m.resumes[r.ID] = &c
	return nil
}

func (m *memRepo) GetResume(_ context.Context, id uuid.UUID) (*Resume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.resumes[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *r
	return &c, nil
}

func (m *memRepo) FindLiveResumeBySHA(_ context.Context, sha string) (*Resume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.resumes {
		if r.SHA256 == sha && r.Status != StatusFailed {
			c := *r
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *memRepo) SetStatus(_ context.Context, id uuid.UUID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resumes[id].Status = status
	m.statuses[id] = append(m.statuses[id], status)
	return nil
}

func (m *memRepo) MarkFailed(_ context.Context, id uuid.UUID, stage, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.resumes[id]
	r.Status, r.FailedStage, r.Error = StatusFailed, stage, msg
	return nil
}

func (m *memRepo) SaveRawText(_ context.Context, id uuid.UUID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rawText[id] = text
	return nil
}

func (m *memRepo) ListUnfinished(context.Context) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for id, r := range m.resumes {
		if r.Status != StatusReady && r.Status != StatusFailed {
			out = append(out, id)
		}
	}
	return out, nil
}

func (m *memRepo) DiscardDraft(_ context.Context, resumeID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.profiles {
		if p.ResumeID == resumeID && !p.IsActive {
			delete(m.profiles, id)
		}
	}
	return nil
}

func (m *memRepo) SaveDraftProfile(_ context.Context, p *Profile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.Version = len(m.profiles) + 1
	c := *p
	m.profiles[p.ID] = &c
	return nil
}

func (m *memRepo) ActivateProfile(_ context.Context, profileID uuid.UUID, facts []Fact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range facts {
		c := f
		c.EmbeddingDim = len(f.Embedding)
		m.facts[f.ID] = &c
	}
	for _, p := range m.profiles {
		p.IsActive = p.ID == profileID
	}
	return nil
}

func (m *memRepo) ActiveProfile(context.Context) (*Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.profiles {
		if p.IsActive {
			c := *p
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *memRepo) GetProfile(_ context.Context, id uuid.UUID) (*Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *p
	return &c, nil
}

func (m *memRepo) ProfileByResume(_ context.Context, resumeID uuid.UUID) (*Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.profiles {
		if p.ResumeID == resumeID {
			c := *p
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *memRepo) CountProfiles(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.profiles), nil
}

func (m *memRepo) ListFacts(_ context.Context, profileID uuid.UUID) ([]Fact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Fact
	for _, f := range m.facts {
		if f.ProfileID == profileID {
			out = append(out, *f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out, nil
}

func (m *memRepo) GetFact(_ context.Context, id uuid.UUID) (*Fact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.facts[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *f
	return &c, nil
}

func (m *memRepo) UpdateFact(_ context.Context, id uuid.UUID, text string, emb []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.facts[id]
	if !ok {
		return ErrNotFound
	}
	f.Text, f.Embedding, f.Edited = text, emb, true
	return nil
}

func (m *memRepo) DeleteFact(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.facts[id]; !ok {
		return ErrNotFound
	}
	delete(m.facts, id)
	return nil
}

func (m *memRepo) SearchFacts(_ context.Context, profileID uuid.UUID, emb []float32, k int) ([]ScoredFact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ScoredFact
	for _, f := range m.facts {
		if f.ProfileID == profileID {
			out = append(out, ScoredFact{Fact: *f, Score: cosine(f.Embedding, emb)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// fakeLLM replies from a script keyed by schema name and embeds text as a
// bag-of-letters vector so similar words land close together.
type fakeLLM struct {
	mu       sync.Mutex
	replies  map[string][]string // schema name -> replies in order
	calls    map[string]int
	requests []llm.ChatRequest
}

func newFakeLLM() *fakeLLM {
	return &fakeLLM{replies: map[string][]string{}, calls: map[string]int{}}
}

func (f *fakeLLM) on(schema string, replies ...string) { f.replies[schema] = replies }

func (f *fakeLLM) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	name := req.ResponseFormat.(map[string]any)["json_schema"].(map[string]any)["name"].(string)
	replies := f.replies[name]
	i := f.calls[name]
	f.calls[name]++
	if len(replies) == 0 {
		return llm.ChatResult{}, errors.New("no scripted reply for " + name)
	}
	if i >= len(replies) {
		i = len(replies) - 1
	}
	return llm.ChatResult{Content: replies[i], PromptTokens: 10, CompletionTokens: 5}, nil
}

func (f *fakeLLM) Embed(_ context.Context, _ string, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		v := make([]float32, 768)
		in = strings.TrimPrefix(strings.TrimPrefix(in, "search_document: "), "search_query: ")
		for _, w := range strings.Fields(strings.ToLower(in)) {
			for _, r := range w {
				v[int(r)%768]++
			}
		}
		out[i] = v
	}
	return out, nil
}

type memObjects struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string][]byte{}
	}
	m.data[key] = data
	return nil
}

func (m *memObjects) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	return d, nil
}

// plainText returns the bytes as text, like Tika does for .txt files.
type plainText struct{ err error }

func (p plainText) ExtractText(_ context.Context, data []byte, _ string) (string, error) {
	return string(data), p.err
}
