//go:build acceptance

// Package acceptance holds the self-validating phase checks. They run against
// the live stack (`make up`) with real local models: `make verify-phase1`.
package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	coreURL    = env("CORE_URL", "http://localhost:8080")
	litellmURL = env("LITELLM_URL", "http://localhost:4000")
	litellmKey = env("LITELLM_KEY", "sk-jobmatcher-local")
	phoenixURL = env("PHOENIX_URL", "http://localhost:6006")
	fixtures   = env("FIXTURES_DIR", "../../../../testdata/fixtures")
	client     = &http.Client{Timeout: 10 * time.Minute}
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// --- report ------------------------------------------------------------------

type result struct {
	name   string
	ok     bool
	detail string
	took   time.Duration
}

var (
	mu      sync.Mutex
	results []result
)

// check runs one named acceptance check as a subtest and records the outcome
// for the summary printed at the end.
func check(t *testing.T, name string, fn func(t *testing.T) string) bool {
	t.Helper()
	start := time.Now()
	var detail string
	ok := t.Run(name, func(t *testing.T) { detail = fn(t) })
	mu.Lock()
	results = append(results, result{name: name, ok: ok, detail: detail, took: time.Since(start)})
	mu.Unlock()
	return ok
}

func TestMain(m *testing.M) {
	code := m.Run()
	printSummary()
	os.Exit(code)
}

func printSummary() {
	mu.Lock()
	defer mu.Unlock()
	if len(results) == 0 {
		return
	}
	passed := 0
	fmt.Println()
	fmt.Println("════════════════════════ PHASE 1 ACCEPTANCE REPORT ════════════════════════")
	for _, r := range results {
		mark := "\033[32m✔ PASS\033[0m"
		if r.ok {
			passed++
		} else {
			mark = "\033[31m✘ FAIL\033[0m"
		}
		fmt.Printf("%s  %-44s %6.1fs  %s\n", mark, r.name, r.took.Seconds(), r.detail)
	}
	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
	if passed == len(results) {
		fmt.Printf("\033[32mAll %d checks passed. Phase 1 is done.\033[0m\n", len(results))
	} else {
		fmt.Printf("\033[31m%d of %d checks failed.\033[0m\n", len(results)-passed, len(results))
	}
}

// --- HTTP helpers ------------------------------------------------------------

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func sendJSON(t *testing.T, method, url string, in, out any) int {
	t.Helper()
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest(method, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(url, litellmURL) {
		req.Header.Set("Authorization", "Bearer "+litellmKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(body, out)
	}
	if resp.StatusCode >= 300 {
		t.Logf("%s %s -> %d: %s", method, url, resp.StatusCode, body)
	}
	return resp.StatusCode
}

type resume struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	FailedStage  string `json:"failed_stage"`
	Error        string `json:"error"`
	ProfileID    string `json:"profile_id"`
	Deduplicated bool   `json:"deduplicated"`
}

func uploadBytes(t *testing.T, filename string, data []byte) (int, resume) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", filename)
	_, _ = part.Write(data)
	_ = w.Close()
	resp, err := client.Post(coreURL+"/v1/resumes", w.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	var r resume
	_ = json.NewDecoder(resp.Body).Decode(&r)
	return resp.StatusCode, r
}

func uploadFile(t *testing.T, path string) (int, resume) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return uploadBytes(t, filepath.Base(path), data)
}

// waitDone polls until the resume is ready or failed.
func waitDone(t *testing.T, id string, timeout time.Duration) resume {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		var r resume
		getJSON(t, coreURL+"/v1/resumes/"+id, &r)
		if r.Status != last {
			t.Logf("  %s -> %s", id[:8], r.Status)
			last = r.Status
		}
		if r.Status == "ready" || r.Status == "failed" {
			return r
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("resume %s not done after %s (last status %s)", id, timeout, last)
	return resume{}
}

type fact struct {
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	Category     string   `json:"category"`
	Skills       []string `json:"skills"`
	Edited       bool     `json:"edited"`
	EmbeddingDim int      `json:"embedding_dim"`
	Score        float64  `json:"score"`
}

type profileResp struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Data    struct {
		Name       string   `json:"name"`
		Email      string   `json:"email"`
		Skills     []string `json:"skills"`
		Experience []struct {
			Company string `json:"company"`
		} `json:"experience"`
	} `json:"data"`
}
