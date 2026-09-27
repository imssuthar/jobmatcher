// Package web serves the single-page Phase 1 UI.
package web

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var index []byte

// Index serves the UI.
func Index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}
