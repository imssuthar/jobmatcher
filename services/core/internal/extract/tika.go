// Package extract turns uploaded documents into plain text using Apache Tika.
package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnreadable means Tika could not parse the document (corrupt or unsupported).
var ErrUnreadable = errors.New("document could not be parsed")

// Tika is a client for the Tika server REST API.
type Tika struct {
	url  string
	http *http.Client
}

// NewTika creates a client for the Tika server at url.
func NewTika(url string) *Tika {
	return &Tika{url: strings.TrimRight(url, "/"), http: &http.Client{Timeout: 2 * time.Minute}}
}

// ExtractText returns the plain text of a document.
func (t *Tika) ExtractText(ctx context.Context, data []byte, contentType string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, t.url+"/tika", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("Content-Type", contentType)
	resp, err := t.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("tika: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("tika: read: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent:
		return string(body), nil
	case resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusUnsupportedMediaType:
		return "", ErrUnreadable
	default:
		return "", fmt.Errorf("tika: status %d", resp.StatusCode)
	}
}

// Ping checks that the Tika server is up.
func (t *Tika) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url+"/version", nil)
	if err != nil {
		return err
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tika: status %d", resp.StatusCode)
	}
	return nil
}
