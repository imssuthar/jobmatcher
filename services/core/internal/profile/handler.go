package profile

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Handler exposes the profile API.
type Handler struct {
	Svc            *Service
	MaxUploadBytes int64
}

// Routes registers the endpoints on r.
func (h *Handler) Routes(r chi.Router) {
	r.Post("/v1/resumes", h.upload)
	r.Get("/v1/resumes/{id}", h.getResume)
	r.Get("/v1/profile", h.activeProfile)
	r.Get("/v1/profile/facts", h.activeFacts)
	r.Get("/v1/profile/facts/search", h.search)
	r.Patch("/v1/profile/facts/{id}", h.updateFact)
	r.Delete("/v1/profile/facts/{id}", h.deleteFact)
	r.Get("/v1/profiles/{id}", h.getProfile)
	r.Get("/v1/profiles/{id}/facts", h.profileFacts)
}

type resumeResponse struct {
	*Resume
	ProfileID    *uuid.UUID `json:"profile_id,omitempty"`
	Deduplicated bool       `json:"deduplicated"`
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxUploadBytes+1<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large")
			return
		}
		writeError(w, http.StatusBadRequest, `multipart field "file" is required`)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, h.MaxUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read upload")
		return
	}
	if int64(len(data)) > h.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}

	res, dedup, err := h.Svc.Submit(r.Context(), header.Filename, data)
	switch {
	case errors.Is(err, ErrUnsupportedType):
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := http.StatusAccepted
	if dedup {
		status = http.StatusOK
	}
	writeJSON(w, status, h.withProfile(r, res, dedup))
}

func (h *Handler) withProfile(r *http.Request, res *Resume, dedup bool) resumeResponse {
	out := resumeResponse{Resume: res, Deduplicated: dedup}
	if p, err := h.Svc.Repo.ProfileByResume(r.Context(), res.ID); err == nil {
		out.ProfileID = &p.ID
	}
	return out
}

func (h *Handler) getResume(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	res, err := h.Svc.Repo.GetResume(r.Context(), id)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.withProfile(r, res, false))
}

func (h *Handler) activeProfile(w http.ResponseWriter, r *http.Request) {
	p, err := h.Svc.Repo.ActiveProfile(r.Context())
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := h.Svc.Repo.GetProfile(r.Context(), id)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) activeFacts(w http.ResponseWriter, r *http.Request) {
	p, err := h.Svc.Repo.ActiveProfile(r.Context())
	if err != nil {
		writeRepoError(w, err)
		return
	}
	h.writeFacts(w, r, p.ID)
}

func (h *Handler) profileFacts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h.writeFacts(w, r, id)
}

func (h *Handler) writeFacts(w http.ResponseWriter, r *http.Request, profileID uuid.UUID) {
	facts, err := h.Svc.Repo.ListFacts(r.Context(), profileID)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile_id": profileID, "count": len(facts), "facts": facts})
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, `query parameter "q" is required`)
		return
	}
	k, _ := strconv.Atoi(r.URL.Query().Get("k"))
	if k <= 0 || k > 20 {
		k = 5
	}
	var profileID uuid.UUID
	if s := r.URL.Query().Get("profile_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid profile_id")
			return
		}
		profileID = id
	} else {
		p, err := h.Svc.Repo.ActiveProfile(r.Context())
		if err != nil {
			writeRepoError(w, err)
			return
		}
		profileID = p.ID
	}
	hits, err := h.Svc.Search(r.Context(), profileID, q, k)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": q, "profile_id": profileID, "results": hits})
}

func (h *Handler) updateFact(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON: {\"text\": \"...\"}")
		return
	}
	f, err := h.Svc.UpdateFact(r.Context(), id, body.Text)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *Handler) deleteFact(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.Svc.Repo.DeleteFact(r.Context(), id); err != nil {
		writeRepoError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}

func writeRepoError(w http.ResponseWriter, err error) {
	var ve *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
