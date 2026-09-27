package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestChatSendsRequestAndParsesUsage(t *testing.T) {
	var got ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("missing auth header")
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"model":"smart","choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":11,"completion_tokens":5}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "key", 5*time.Second)
	req := Deterministic(ChatRequest{Model: "smart", Messages: []Message{{Role: "user", Content: "hi"}}})
	req.ResponseFormat = JSONSchemaFormat("x", map[string]any{"type": "object"})
	res, err := c.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != `{"ok":true}` || res.PromptTokens != 11 || res.CompletionTokens != 5 {
		t.Fatalf("unexpected result %+v", res)
	}
	if got.Temperature == nil || *got.Temperature != 0 || got.Seed == nil || *got.Seed != 42 {
		t.Fatalf("deterministic settings not sent: %+v", got)
	}
	if got.ResponseFormat == nil {
		t.Fatal("response_format not sent")
	}
}

func TestChatRetriesTransientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", 5*time.Second)
	res, err := c.Chat(context.Background(), ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "ok" || calls.Load() != 2 {
		t.Fatalf("content=%q calls=%d", res.Content, calls.Load())
	}
}

func TestChatDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "", time.Second).Chat(context.Background(), ChatRequest{Model: "m"}); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestEmbedReordersByIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[2]},{"index":0,"embedding":[1]}]}`))
	}))
	defer srv.Close()

	out, err := New(srv.URL, "", time.Second).Embed(context.Background(), "embed", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if out[0][0] != 1 || out[1][0] != 2 {
		t.Fatalf("wrong order: %v", out)
	}
}

func TestEmbedCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1]}]}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "", time.Second).Embed(context.Background(), "embed", []string{"a", "b"}); err == nil {
		t.Fatal("expected count mismatch error")
	}
}
