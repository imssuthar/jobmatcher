// Package llm is a small client for the OpenAI-compatible API exposed by the
// LiteLLM gateway. Services only ever talk to the gateway, never to a model
// runtime directly, so swapping Ollama for anything else is a config change.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the subset of the chat completions API we use.
type ChatRequest struct {
	Model          string    `json:"model"`
	Messages       []Message `json:"messages"`
	Temperature    *float64  `json:"temperature,omitempty"`
	Seed           *int      `json:"seed,omitempty"`
	MaxTokens      int       `json:"max_tokens,omitempty"`
	ResponseFormat any       `json:"response_format,omitempty"`
}

// ChatResult is the assistant reply plus usage accounting.
type ChatResult struct {
	Content          string
	Model            string
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
}

// Client calls the gateway. It is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	tracer  trace.Tracer
	retries int
}

// New creates a client for the gateway at baseURL.
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
		tracer:  otel.Tracer("jobmatcher/llm"),
		retries: 3,
	}
}

// Deterministic returns sampling settings for reproducible extraction.
func Deterministic(req ChatRequest) ChatRequest {
	t, s := 0.0, 42
	req.Temperature, req.Seed = &t, &s
	return req
}

// JSONSchemaFormat builds a response_format that constrains output to schema.
func JSONSchemaFormat(name string, schema map[string]any) map[string]any {
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   name,
			"schema": schema,
			"strict": true,
		},
	}
}

// Chat sends a chat completion request.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	ctx, span := c.tracer.Start(ctx, "llm.chat "+req.Model)
	defer span.End()
	span.SetAttributes(
		attribute.String("openinference.span.kind", "LLM"),
		attribute.String("llm.model_name", req.Model),
		attribute.String("input.value", truncate(lastUserMessage(req.Messages), 8000)),
	)

	var resp struct {
		Model   string `json:"model"`
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	start := time.Now()
	if err := c.post(ctx, "/v1/chat/completions", req, &resp); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return ChatResult{}, err
	}
	if len(resp.Choices) == 0 {
		err := errors.New("llm: response has no choices")
		span.SetStatus(codes.Error, err.Error())
		return ChatResult{}, err
	}
	res := ChatResult{
		Content:          resp.Choices[0].Message.Content,
		Model:            resp.Model,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		Latency:          time.Since(start),
	}
	span.SetAttributes(
		attribute.String("output.value", truncate(res.Content, 8000)),
		attribute.Int("llm.token_count.prompt", res.PromptTokens),
		attribute.Int("llm.token_count.completion", res.CompletionTokens),
		attribute.Int("llm.token_count.total", res.PromptTokens+res.CompletionTokens),
	)
	return res, nil
}

// Embed returns one embedding per input, in input order.
func (c *Client) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	ctx, span := c.tracer.Start(ctx, "llm.embed "+model)
	defer span.End()
	span.SetAttributes(
		attribute.String("openinference.span.kind", "EMBEDDING"),
		attribute.String("embedding.model_name", model),
		attribute.Int("embedding.count", len(inputs)),
	)

	var resp struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	body := map[string]any{"model": model, "input": inputs}
	if err := c.post(ctx, "/v1/embeddings", body, &resp); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if len(resp.Data) != len(inputs) {
		return nil, fmt.Errorf("llm: asked for %d embeddings, got %d", len(inputs), len(resp.Data))
	}
	out := make([][]float32, len(inputs))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("llm: embedding index %d out of range", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

// Ping checks that the gateway process is alive.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health/liveliness", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("llm gateway: status %d", resp.StatusCode)
	}
	return nil
}

// post sends a JSON request, retrying transient failures (network errors,
// 429 and 5xx) with exponential backoff.
func (c *Client) post(ctx context.Context, path string, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	var lastErr error
	backoff := time.Second
	for attempt := 0; attempt < c.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		retry, err := c.do(ctx, path, payload, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			return err
		}
	}
	return lastErr
}

func (c *Client) do(ctx context.Context, path string, payload []byte, out any) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return true, fmt.Errorf("llm %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return true, fmt.Errorf("llm %s: read body: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		retry = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return retry, fmt.Errorf("llm %s: status %d: %s", path, resp.StatusCode, truncate(string(body), 500))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return false, fmt.Errorf("llm %s: decode: %w", path, err)
	}
	return false, nil
}

func lastUserMessage(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
