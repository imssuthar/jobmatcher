//go:build acceptance

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/imssuthar/jobmatcher/services/core/internal/evalkit"
	"github.com/imssuthar/jobmatcher/services/core/internal/profile"
)

// Thresholds for "Phase 1 works". LLM output varies, so quality is asserted
// with recall and grounding rates rather than exact matches.
const (
	minSkillRecall    = 0.8
	minCompanyRecall  = 1.0
	minNumberGrounded = 0.9
	// Recall@3 over each fixture's search probes. Pure vector search with a
	// small embedding model misses some paraphrases (e.g. "continuous
	// delivery" vs "CI/CD"); Phase 3 adds hybrid search and reranking.
	minSearchRecall = 2.0 / 3.0
	processTimeout    = 10 * time.Minute
)

func TestPhase1(t *testing.T) {
	golden, err := evalkit.Load(fixtures)
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}

	if !check(t, "01 stack healthy + models respond", checkStack) {
		t.Fatal("stack is not healthy; run `make up` and `make logs`")
	}

	profiles := map[string]string{} // fixture id -> profile id
	resumes := map[string]string{}  // fixture id -> resume id
	for _, f := range golden.Fixtures {
		ext := strings.TrimPrefix(filepath.Ext(f.Upload), ".")
		check(t, fmt.Sprintf("02 process %s (%s)", f.ID, ext), func(t *testing.T) string {
			start := time.Now()
			code, r := uploadFile(t, golden.UploadPath(f))
			if code != http.StatusAccepted && code != http.StatusOK {
				t.Fatalf("upload status %d", code)
			}
			done := waitDone(t, r.ID, processTimeout)
			if done.Status != "ready" {
				t.Fatalf("status %s at %s: %s", done.Status, done.FailedStage, done.Error)
			}
			resumes[f.ID], profiles[f.ID] = done.ID, done.ProfileID
			if r.Deduplicated {
				return "already processed (dedup)"
			}
			return fmt.Sprintf("ready in %.0fs", time.Since(start).Seconds())
		})
	}

	for _, f := range golden.Fixtures {
		pid := profiles[f.ID]
		if pid == "" {
			continue
		}
		src, _ := golden.SourceText(f)
		check(t, "03 profile fields "+f.ID, func(t *testing.T) string { return checkProfile(t, f, pid) })
		check(t, "04 fact bank "+f.ID, func(t *testing.T) string { return checkFacts(t, f, pid, src) })
		check(t, "05 semantic search "+f.ID, func(t *testing.T) string { return checkSearch(t, f, pid) })
	}

	check(t, "06 same file is deduplicated", func(t *testing.T) string {
		f := golden.Fixtures[0]
		if resumes[f.ID] == "" {
			t.Skip("first fixture did not process")
		}
		var before profileResp
		getJSON(t, coreURL+"/v1/profile", &before)
		code, r := uploadFile(t, golden.UploadPath(f))
		var after profileResp
		getJSON(t, coreURL+"/v1/profile", &after)
		if code != http.StatusOK || !r.Deduplicated || r.ID != resumes[f.ID] {
			t.Fatalf("expected 200 dedup to %s, got %d dedup=%v id=%s", resumes[f.ID], code, r.Deduplicated, r.ID)
		}
		if before.Version != after.Version {
			t.Fatalf("active profile version changed %d -> %d", before.Version, after.Version)
		}
		return "no reprocessing, no new version"
	})

	check(t, "07 editing a fact re-embeds it", checkEditFact)

	check(t, "08 corrupt file fails cleanly", func(t *testing.T) string {
		code, r := uploadFile(t, filepath.Join(fixtures, "corrupt.pdf"))
		if code != http.StatusAccepted && code != http.StatusOK {
			t.Fatalf("upload status %d", code)
		}
		done := waitDone(t, r.ID, 2*time.Minute)
		if done.Status != "failed" || done.FailedStage != "parsing" || done.Error == "" {
			t.Fatalf("expected failed at parsing with an error, got %+v", done)
		}
		if code := getJSON(t, coreURL+"/readyz", nil); code != http.StatusOK {
			t.Fatalf("service unhealthy after failure: %d", code)
		}
		return "failed at parsing: " + truncate(done.Error, 50)
	})

	check(t, "09 unsupported file type rejected", func(t *testing.T) string {
		code, _ := uploadBytes(t, "photo.png", []byte("\x89PNG\r\n\x1a\nnot a resume"))
		if code != http.StatusUnsupportedMediaType {
			t.Fatalf("expected 415, got %d", code)
		}
		return "415 Unsupported Media Type"
	})

	check(t, "10 LLM calls traced in Phoenix", checkTraces)
}

func checkStack(t *testing.T) string {
	var ready map[string]string
	if code := getJSON(t, coreURL+"/readyz", &ready); code != http.StatusOK {
		t.Fatalf("core /readyz %d: %v", code, ready)
	}
	for _, alias := range []string{"smart", "fast"} {
		var out struct {
			Choices []struct {
				Message struct{ Content string } `json:"message"`
			} `json:"choices"`
		}
		body := map[string]any{"model": alias, "max_tokens": 5, "messages": []map[string]string{{"role": "user", "content": "Reply with OK"}}}
		if code := sendJSON(t, http.MethodPost, litellmURL+"/v1/chat/completions", body, &out); code != 200 || len(out.Choices) == 0 {
			t.Fatalf("model %s did not answer (status %d)", alias, code)
		}
	}
	var emb struct {
		Data []struct{ Embedding []float64 } `json:"data"`
	}
	sendJSON(t, http.MethodPost, litellmURL+"/v1/embeddings", map[string]any{"model": "embed", "input": []string{"hello"}}, &emb)
	if len(emb.Data) != 1 || len(emb.Data[0].Embedding) != 768 {
		t.Fatalf("embed model must return 768 dims")
	}
	return "postgres, s3, tika, gateway ok; smart+fast+embed respond"
}

func checkProfile(t *testing.T, f evalkit.Fixture, pid string) string {
	var p profileResp
	if code := getJSON(t, coreURL+"/v1/profiles/"+pid, &p); code != 200 {
		t.Fatalf("GET profile: %d", code)
	}
	if !strings.EqualFold(p.Data.Name, f.Name) {
		t.Errorf("name = %q, want %q", p.Data.Name, f.Name)
	}
	if p.Data.Email != f.Email {
		t.Errorf("email = %q, want %q", p.Data.Email, f.Email)
	}
	skillRecall, missSkills := evalkit.Recall(f.Skills, p.Data.Skills)
	if skillRecall < minSkillRecall {
		t.Errorf("skill recall %.0f%% < %.0f%%, missing %v (got %v)", skillRecall*100, minSkillRecall*100, missSkills, p.Data.Skills)
	}
	var companies []string
	for _, e := range p.Data.Experience {
		companies = append(companies, e.Company)
	}
	coRecall, missCo := evalkit.Recall(f.Companies, companies)
	if coRecall < minCompanyRecall {
		t.Errorf("company recall %.0f%%, missing %v", coRecall*100, missCo)
	}
	return fmt.Sprintf("name+email ok, skills %.0f%%, companies %.0f%%", skillRecall*100, coRecall*100)
}

func checkFacts(t *testing.T, f evalkit.Fixture, pid, src string) string {
	var out struct {
		Count int    `json:"count"`
		Facts []fact `json:"facts"`
	}
	getJSON(t, coreURL+"/v1/profiles/"+pid+"/facts", &out)
	if out.Count < f.FactsMin || out.Count > f.FactsMax {
		t.Errorf("fact count %d outside [%d, %d]", out.Count, f.FactsMin, f.FactsMax)
	}
	texts := make([]string, 0, len(out.Facts))
	for _, fa := range out.Facts {
		if fa.EmbeddingDim != 768 {
			t.Errorf("fact %q has embedding dim %d", truncate(fa.Text, 40), fa.EmbeddingDim)
		}
		if !slices.Contains(profile.FactCategories, fa.Category) {
			t.Errorf("fact %q has category %q", truncate(fa.Text, 40), fa.Category)
		}
		texts = append(texts, fa.Text)
	}
	g := profile.NumberGrounding(src, texts)
	if g.Rate() < minNumberGrounded {
		t.Errorf("only %.0f%% of numbers in facts appear in the resume; invented: %v", g.Rate()*100, g.Ungrounded)
	}
	return fmt.Sprintf("%d facts, all embedded, %d/%d numbers grounded", out.Count, g.NumbersGrounded, g.NumbersTotal)
}

func checkSearch(t *testing.T, f evalkit.Fixture, pid string) string {
	hits := 0
	var misses []string
	for _, probe := range f.Search {
		var out struct{ Results []fact }
		getJSON(t, fmt.Sprintf("%s/v1/profile/facts/search?profile_id=%s&k=3&q=%s", coreURL, pid, url.QueryEscape(probe.Query)), &out)
		found := false
		for _, r := range out.Results {
			if strings.Contains(strings.ToLower(r.Text), strings.ToLower(probe.Expect)) {
				found = true
				break
			}
		}
		if found {
			hits++
			continue
		}
		misses = append(misses, fmt.Sprintf("%q", probe.Query))
		top := []string{}
		for _, r := range out.Results {
			top = append(top, truncate(r.Text, 60))
		}
		t.Logf("miss: query %q: no top-3 fact mentions %q; got %v", probe.Query, probe.Expect, top)
	}
	recall := float64(hits) / float64(len(f.Search))
	if recall < minSearchRecall {
		t.Errorf("recall@3 %.0f%% < %.0f%%", recall*100, minSearchRecall*100)
	}
	detail := fmt.Sprintf("recall@3 %d/%d", hits, len(f.Search))
	if len(misses) > 0 {
		detail += ", missed " + strings.Join(misses, ", ")
	}
	return detail
}

func checkEditFact(t *testing.T) string {
	var out struct{ Facts []fact }
	if code := getJSON(t, coreURL+"/v1/profile/facts", &out); code != 200 || len(out.Facts) == 0 {
		t.Fatalf("no active facts (status %d)", code)
	}
	target := out.Facts[len(out.Facts)-1]
	const probe = "Designed a lunar greenhouse irrigation controller written in Rust for a hackathon."
	var updated fact
	if code := sendJSON(t, http.MethodPatch, coreURL+"/v1/profile/facts/"+target.ID, map[string]string{"text": probe}, &updated); code != 200 {
		t.Fatalf("PATCH status %d", code)
	}
	defer sendJSON(t, http.MethodPatch, coreURL+"/v1/profile/facts/"+target.ID, map[string]string{"text": target.Text}, nil)

	if !updated.Edited || updated.EmbeddingDim != 768 {
		t.Fatalf("fact not marked edited/re-embedded: %+v", updated)
	}
	var res struct{ Results []fact }
	getJSON(t, coreURL+"/v1/profile/facts/search?k=1&q="+url.QueryEscape("lunar greenhouse irrigation"), &res)
	if len(res.Results) == 0 || res.Results[0].ID != target.ID {
		t.Fatalf("search did not return the edited fact first: %+v", res.Results)
	}
	var bad map[string]string
	if code := sendJSON(t, http.MethodPatch, coreURL+"/v1/profile/facts/"+target.ID, map[string]string{"text": "tiny"}, &bad); code != http.StatusBadRequest {
		t.Fatalf("too-short edit should be 400, got %d", code)
	}
	return fmt.Sprintf("edited fact is top-1 for its new text (score %.2f)", res.Results[0].Score)
}

// checkTraces asks Phoenix, over its GraphQL API, whether the jobmatcher
// project has recorded traces.
func checkTraces(t *testing.T) string {
	query := map[string]string{"query": `{ projects { edges { node { name traceCount } } } }`}
	var out struct {
		Data struct {
			Projects struct {
				Edges []struct {
					Node struct {
						Name       string
						TraceCount int
					}
				}
			}
		}
	}
	// Spans are exported in batches; give the exporter a moment.
	for i := 0; i < 10; i++ {
		sendJSON(t, http.MethodPost, phoenixURL+"/graphql", query, &out)
		for _, e := range out.Data.Projects.Edges {
			if e.Node.Name == "jobmatcher" && e.Node.TraceCount > 0 {
				return fmt.Sprintf("%d traces in project jobmatcher", e.Node.TraceCount)
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("no traces for project jobmatcher in Phoenix (%s)", phoenixURL)
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
