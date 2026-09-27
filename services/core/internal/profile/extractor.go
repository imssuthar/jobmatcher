package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/imssuthar/jobmatcher/services/core/internal/llm"
)

// LLM is the part of the gateway client the pipeline needs.
type LLM interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResult, error)
	Embed(ctx context.Context, model string, inputs []string) ([][]float32, error)
}

// Extractor runs the two LLM extraction steps with validation and retries.
type Extractor struct {
	LLM          LLM
	ProfileModel string
	FactsModel   string
	// MaxRetries is how many times a rejected answer is sent back for correction.
	MaxRetries int
}

// Usage records what an extraction cost.
type Usage struct {
	Attempts         int
	PromptTokens     int
	CompletionTokens int
}

func (u *Usage) add(r llm.ChatResult) {
	u.Attempts++
	u.PromptTokens += r.PromptTokens
	u.CompletionTokens += r.CompletionTokens
}

// ExtractProfile turns resume text into a validated ProfileData.
func (x *Extractor) ExtractProfile(ctx context.Context, text string) (ProfileData, Usage, error) {
	return chatJSON(ctx, x, x.ProfileModel, "profile", ProfileSchema(), profileSystemPrompt, profileUserPrompt(text),
		func(p *ProfileData) error { p.Normalize(); return p.Validate(text) })
}

// ExtractFacts builds the fact bank for a resume.
func (x *Extractor) ExtractFacts(ctx context.Context, text string, p ProfileData) (FactsData, Usage, error) {
	return chatJSON(ctx, x, x.FactsModel, "facts", FactsSchema(), factsSystemPrompt, factsUserPrompt(text, p),
		func(f *FactsData) error { f.Normalize(); return f.Validate() })
}

// chatJSON asks for schema-constrained JSON, decodes it into a fresh T and
// runs check. Rejected answers are sent back with the problems listed so the
// model can correct itself.
func chatJSON[T any](ctx context.Context, x *Extractor, model, name string, schema map[string]any, system, user string, check func(*T) error) (T, Usage, error) {
	var u Usage
	msgs := []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	var lastErr error
	for attempt := 0; attempt <= x.MaxRetries; attempt++ {
		req := llm.Deterministic(llm.ChatRequest{Model: model, Messages: msgs, MaxTokens: 4096})
		req.ResponseFormat = llm.JSONSchemaFormat(name, schema)
		res, err := x.LLM.Chat(ctx, req)
		if err != nil {
			var zero T
			return zero, u, err // transport errors are already retried by the client
		}
		u.add(res)
		var out T
		if lastErr = decode(res.Content, &out); lastErr == nil {
			lastErr = check(&out)
		}
		if lastErr == nil {
			return out, u, nil
		}
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: res.Content},
			llm.Message{Role: "user", Content: retryPrompt(lastErr)})
	}
	var zero T
	return zero, u, fmt.Errorf("%s extraction failed after %d attempts: %w", name, u.Attempts, lastErr)
}

// decode parses JSON, tolerating a markdown code fence around it.
func decode(content string, out any) error {
	s := strings.TrimSpace(content)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	if err := json.Unmarshal([]byte(s), out); err != nil {
		return &ValidationError{Problems: []string{"output is not valid JSON: " + err.Error()}}
	}
	return nil
}
