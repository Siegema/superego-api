package work

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
)

// entryStore is the persistence boundary consumed by the HTTP handlers.
type entryStore interface {
	CreateEntry(context.Context, string, EntryInput) (Entry, error)
	GetEntry(context.Context, string, int64) (Entry, error)
	ListEntries(context.Context, string, int64, int) ([]Entry, error)
	UpdateEntry(context.Context, string, int64, EntryInput) (Entry, error)
	DeleteEntry(context.Context, string, int64) error
}

type Handler struct {
	store entryStore
	owner string
}

// NewHandler uses a server-supplied identity, never one from the request body.
// The fixed owner is only for the local development server until auth is added.
func NewHandler(store entryStore, owner string) *Handler {
	if owner == "" {
		panic("work: an owner is required")
	}
	return &Handler{store: store, owner: owner}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write entry response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func repositoryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry not found")
		return
	}
	// Do not return SQL, connection details, or other internal errors to clients.
	slog.Error("entry persistence failed", "error", err)
	writeError(w, http.StatusInternalServerError, "unable to process entry")
}

func readInput(w http.ResponseWriter, r *http.Request) (EntryInput, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return EntryInput{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input *EntryInput
	err = decoder.Decode(&input)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); trailing != io.EOF {
			err = trailing
			if err == nil {
				err = errors.New("expected a single JSON object")
			}
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "expected one JSON object with title, notes, and optional RFC3339 occurred_at")
		}
		return EntryInput{}, false
	}
	if input == nil {
		writeError(w, http.StatusBadRequest, "entry must be a JSON object")
		return EntryInput{}, false
	}
	if err := input.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return EntryInput{}, false
	}
	return *input, true
}

func entryID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

func (h *Handler) CreateEntry(w http.ResponseWriter, r *http.Request) {
	input, ok := readInput(w, r)
	if !ok {
		return
	}
	entry, err := h.store.CreateEntry(r.Context(), h.owner, input)
	if err != nil {
		repositoryError(w, err)
		return
	}
	w.Header().Set("Location", "/api/entries/"+strconv.FormatInt(entry.ID, 10))
	writeJSON(w, http.StatusCreated, entry)
}

func (h *Handler) GetEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := entryID(w, r)
	if !ok {
		return
	}
	entry, err := h.store.GetEntry(r.Context(), h.owner, id)
	if err != nil {
		repositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (h *Handler) ListEntries(w http.ResponseWriter, r *http.Request) {
	limit := 50
	var beforeID int64
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid query parameters")
		return
	}
	for key, values := range query {
		if (key != "limit" && key != "before_id") || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "supported query parameters are limit and before_id, each at most once")
			return
		}
	}
	if query.Has("limit") {
		value, err := strconv.Atoi(query.Get("limit"))
		if err != nil || value < 1 || value > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = value
	}
	if query.Has("before_id") {
		value, err := strconv.ParseInt(query.Get("before_id"), 10, 64)
		if err != nil || value < 1 {
			writeError(w, http.StatusBadRequest, "before_id must be a positive integer")
			return
		}
		beforeID = value
	}
	entries, err := h.store.ListEntries(r.Context(), h.owner, beforeID, limit+1)
	if err != nil {
		repositoryError(w, err)
		return
	}
	var next *string
	if len(entries) > limit {
		entries = entries[:limit]
		value := strconv.FormatInt(entries[len(entries)-1].ID, 10)
		next = &value
	}
	if entries == nil {
		entries = []Entry{}
	}
	writeJSON(w, http.StatusOK, struct {
		Entries      []Entry `json:"entries"`
		NextBeforeID *string `json:"next_before_id"`
	}{entries, next})
}

func (h *Handler) UpdateEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := entryID(w, r)
	if !ok {
		return
	}
	input, ok := readInput(w, r)
	if !ok {
		return
	}
	entry, err := h.store.UpdateEntry(r.Context(), h.owner, id, input)
	if err != nil {
		repositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (h *Handler) DeleteEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := entryID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteEntry(r.Context(), h.owner, id); err != nil {
		repositoryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
