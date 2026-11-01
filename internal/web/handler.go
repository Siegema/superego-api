// Package web serves entry pages using HTML templates and ordinary browser forms.
package web

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/siegema/superego-api/assets"
	"github.com/siegema/superego-api/internal/work"
	"github.com/siegema/superego-api/web"
)

type entryStore interface {
	CreateEntry(context.Context, string, work.EntryInput) (work.Entry, error)
	GetEntry(context.Context, string, int64) (work.Entry, error)
	ListEntries(context.Context, string, int64, int) ([]work.Entry, error)
	UpdateEntry(context.Context, string, int64, work.EntryInput) (work.Entry, error)
	DeleteEntry(context.Context, string, int64) error
}

type handler struct {
	store entryStore
	owner string
	pages *template.Template
}

type entryForm struct {
	Title      string
	Notes      string
	OccurredAt string
}

type page struct {
	Title        string
	Error        string
	Entry        work.Entry
	Entries      []work.Entry
	Form         entryForm
	Action       string
	Editing      bool
	BeforeID     int64
	NextBeforeID int64
}

// NewHandler shares the API's repository and server-supplied development identity.
// It makes no HTTP calls to the JSON API and loads no files at request time.
func NewHandler(store entryStore, owner string) (http.Handler, error) {
	if owner == "" {
		return nil, errors.New("web: an owner is required")
	}
	pages, err := template.New("pages").Funcs(template.FuncMap{
		"entryPath": entryPath,
		"timestamp": func(t time.Time) string { return t.UTC().Format("Jan 2, 2006 at 15:04 UTC") },
	}).ParseFS(templates.Files, "*.html")
	if err != nil {
		return nil, err
	}
	h := &handler{store: store, owner: owner, pages: pages}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/entries", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /entries", h.list)
	mux.HandleFunc("GET /entries/new", h.newEntry)
	mux.HandleFunc("POST /entries", h.create)
	mux.HandleFunc("GET /entries/{id}", h.view)
	mux.HandleFunc("GET /entries/{id}/edit", h.edit)
	mux.HandleFunc("POST /entries/{id}", h.update)
	mux.HandleFunc("GET /entries/{id}/delete", h.confirmDelete)
	mux.HandleFunc("POST /entries/{id}/delete", h.delete)
	mux.HandleFunc("GET /assets/style.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, assets.Files, "style.css")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	}), nil
}

func entryPath(id int64) string {
	return "/entries/" + strconv.FormatInt(id, 10)
}

func (h *handler) render(w http.ResponseWriter, status int, name string, data page) {
	var body bytes.Buffer
	if err := h.pages.ExecuteTemplate(&body, name, data); err != nil {
		slog.Error("render entry page", "error", err)
		http.Error(w, "Unable to display this page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body.Bytes()); err != nil {
		slog.Error("write entry page", "error", err)
	}
}

func (h *handler) failure(w http.ResponseWriter, status int, message string) {
	h.render(w, status, "error", page{Title: "Unable to open this memory", Error: message})
}

func (h *handler) storageFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, work.ErrNotFound) {
		h.failure(w, http.StatusNotFound, "This memory could not be found.")
		return
	}
	slog.Error("website entry persistence failed", "error", err)
	h.failure(w, http.StatusInternalServerError, "Unable to load your memories. Please try again.")
}

func (h *handler) loadEntry(w http.ResponseWriter, r *http.Request) (work.Entry, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		h.failure(w, http.StatusBadRequest, "The memory ID must be a positive integer.")
		return work.Entry{}, false
	}
	entry, err := h.store.GetEntry(r.Context(), h.owner, id)
	if err != nil {
		h.storageFailure(w, err)
		return work.Entry{}, false
	}
	return entry, true
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 || (len(query) == 1 && (!query.Has("before_id") || len(query["before_id"]) != 1)) {
		h.failure(w, http.StatusBadRequest, "Use only a single before_id pagination cursor.")
		return
	}
	var beforeID int64
	if query.Has("before_id") {
		beforeID, err = strconv.ParseInt(query.Get("before_id"), 10, 64)
		if err != nil || beforeID < 1 {
			h.failure(w, http.StatusBadRequest, "The pagination cursor must be a positive integer.")
			return
		}
	}
	const pageSize = 20
	entries, err := h.store.ListEntries(r.Context(), h.owner, beforeID, pageSize+1)
	if err != nil {
		h.storageFailure(w, err)
		return
	}
	data := page{Title: "Your work memories", Entries: entries, BeforeID: beforeID}
	if len(entries) > pageSize {
		data.Entries = entries[:pageSize]
		data.NextBeforeID = data.Entries[pageSize-1].ID
	}
	h.render(w, http.StatusOK, "list", data)
}

func (h *handler) newEntry(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, "form", page{Title: "Record a memory", Action: "/entries"})
}

func (h *handler) view(w http.ResponseWriter, r *http.Request) {
	if entry, ok := h.loadEntry(w, r); ok {
		h.render(w, http.StatusOK, "entry", page{Title: entry.Title, Entry: entry})
	}
}

func (h *handler) edit(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.loadEntry(w, r)
	if !ok {
		return
	}
	form := entryForm{Title: entry.Title, Notes: entry.Notes}
	if entry.OccurredAt != nil {
		// Native HTML time controls accept milliseconds, not PostgreSQL's
		// microseconds. save preserves the original if this value is unchanged.
		form.OccurredAt = entry.OccurredAt.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.999")
	}
	h.render(w, http.StatusOK, "form", page{Title: "Edit memory", Entry: entry, Form: form, Action: entryPath(entry.ID), Editing: true})
}

// readForm only reads body fields, so URL parameters cannot override form input.
func (h *handler) readForm(w http.ResponseWriter, r *http.Request) (entryForm, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		h.failure(w, http.StatusUnsupportedMediaType, "Submit this entry using the website form.")
		return entryForm{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.failure(w, http.StatusRequestEntityTooLarge, "This form is too large to submit.")
		} else {
			h.failure(w, http.StatusBadRequest, "Unable to read this form.")
		}
		return entryForm{}, false
	}
	for key, values := range r.PostForm {
		if (key != "title" && key != "notes" && key != "occurred_at") || len(values) != 1 {
			h.failure(w, http.StatusBadRequest, "This form contains unsupported or repeated fields.")
			return entryForm{}, false
		}
	}
	return entryForm{
		Title: r.PostForm.Get("title"), Notes: r.PostForm.Get("notes"), OccurredAt: r.PostForm.Get("occurred_at"),
	}, true
}

func (f entryForm) input() (work.EntryInput, error) {
	input := work.EntryInput{Title: f.Title, Notes: f.Notes}
	if f.OccurredAt != "" {
		var parsed time.Time
		var err error
		for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05.999999999"} {
			parsed, err = time.Parse(layout, f.OccurredAt)
			if err == nil {
				break
			}
		}
		if err != nil {
			return input, errors.New("Enter a valid occurrence date and time in UTC, or leave it blank.")
		}
		input.OccurredAt = &parsed
	}
	err := input.Validate()
	return input, err
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	h.save(w, r, page{Title: "Record a memory", Action: "/entries"})
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	if entry, ok := h.loadEntry(w, r); ok {
		h.save(w, r, page{Title: "Edit memory", Entry: entry, Action: entryPath(entry.ID), Editing: true})
	}
}

func (h *handler) save(w http.ResponseWriter, r *http.Request, data page) {
	form, ok := h.readForm(w, r)
	if !ok {
		return
	}
	data.Form = form
	input, err := form.input()
	if err != nil {
		data.Error = err.Error()
		h.render(w, http.StatusBadRequest, "form", data)
		return
	}
	var entry work.Entry
	if data.Editing {
		if input.OccurredAt != nil && data.Entry.OccurredAt != nil && input.OccurredAt.Equal(data.Entry.OccurredAt.Truncate(time.Millisecond)) {
			input.OccurredAt = data.Entry.OccurredAt
		}
		entry, err = h.store.UpdateEntry(r.Context(), h.owner, data.Entry.ID, input)
	} else {
		entry, err = h.store.CreateEntry(r.Context(), h.owner, input)
	}
	if errors.Is(err, work.ErrNotFound) {
		h.storageFailure(w, err)
		return
	}
	if err != nil {
		slog.Error("save website entry", "error", err)
		data.Error = "Your memory could not be saved. Your text is still below; please try again."
		h.render(w, http.StatusInternalServerError, "form", data)
		return
	}
	http.Redirect(w, r, entryPath(entry.ID), http.StatusSeeOther)
}

func (h *handler) confirmDelete(w http.ResponseWriter, r *http.Request) {
	if entry, ok := h.loadEntry(w, r); ok {
		h.render(w, http.StatusOK, "delete", page{Title: "Delete memory?", Entry: entry})
	}
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.loadEntry(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteEntry(r.Context(), h.owner, entry.ID); err != nil {
		h.storageFailure(w, err)
		return
	}
	http.Redirect(w, r, "/entries", http.StatusSeeOther)
}
