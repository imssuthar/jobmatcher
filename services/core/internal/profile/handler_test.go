package profile

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func newTestServer(t *testing.T, maxBytes int64) (*httptest.Server, *memRepo) {
	t.Helper()
	fake := newFakeLLM()
	fake.on("profile", validProfileJSON())
	fake.on("facts", validFactsJSON())
	svc, repo, _ := newTestService(t, fake, plainText{})
	r := chi.NewRouter()
	(&Handler{Svc: svc, MaxUploadBytes: maxBytes}).Routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, repo
}

func upload(t *testing.T, url, filename string, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", filename)
	_, _ = part.Write(data)
	_ = w.Close()
	resp, err := http.Post(url+"/v1/resumes", w.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestUploadStatusCodes(t *testing.T) {
	srv, repo := newTestServer(t, 1<<20)

	resp := upload(t, srv.URL, "jane.txt", []byte(sampleResume))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first upload: %d", resp.StatusCode)
	}
	var created resumeResponse
	_ = json.NewDecoder(resp.Body).Decode(&created)
	waitTerminal(t, repo, created.ID)

	if resp := upload(t, srv.URL, "jane.txt", []byte(sampleResume)); resp.StatusCode != http.StatusOK {
		t.Fatalf("duplicate upload: %d", resp.StatusCode)
	}
	if resp := upload(t, srv.URL, "photo.png", []byte("\x89PNG....")); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("png upload: %d", resp.StatusCode)
	}
}

func TestUploadTooLarge(t *testing.T) {
	srv, _ := newTestServer(t, 100)
	if resp := upload(t, srv.URL, "big.txt", bytes.Repeat([]byte("a"), 5000)); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestReadEndpoints(t *testing.T) {
	srv, _ := newTestServer(t, 1<<20)

	if resp, _ := http.Get(srv.URL + "/v1/profile"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("profile before upload: %d", resp.StatusCode)
	}
	if resp, _ := http.Get(srv.URL + "/v1/resumes/not-a-uuid"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad id: %d", resp.StatusCode)
	}
	if resp, _ := http.Get(srv.URL + "/v1/profile/facts/search"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("search without q: %d", resp.StatusCode)
	}
}
