package web_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/siegema/superego-api/internal/server"
	"github.com/siegema/superego-api/internal/web"
	"github.com/siegema/superego-api/internal/work"
)

type call struct {
	method, owner string
	id            int64
	limit         int
}

type store struct {
	mu      sync.Mutex
	calls   []call
	entry   work.Entry
	entries []work.Entry
	input   work.EntryInput
	err     error
	saveErr error
}

func (s *store) CreateEntry(_ context.Context, owner string, input work.EntryInput) (work.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{method: "create", owner: owner})
	s.input = input
	return s.entry, s.saveErr
}

func (s *store) GetEntry(_ context.Context, owner string, id int64) (work.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{method: "get", owner: owner, id: id})
	return s.entry, s.err
}

func (s *store) ListEntries(_ context.Context, owner string, before int64, limit int) ([]work.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{method: "list", owner: owner, id: before, limit: limit})
	return s.entries, s.err
}

func (s *store) UpdateEntry(_ context.Context, owner string, id int64, input work.EntryInput) (work.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{method: "update", owner: owner, id: id})
	s.input = input
	return s.entry, s.saveErr
}

func (s *store) DeleteEntry(_ context.Context, owner string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{method: "delete", owner: owner, id: id})
	return s.saveErr
}

func site(t *testing.T, s *store) http.Handler {
	t.Helper()
	pages, err := web.NewHandler(s, "owner")
	if err != nil {
		t.Fatal(err)
	}
	return server.NewHandler(work.NewHandler(s, "owner"), pages)
}

func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func form(title, notes, occurred string) string {
	return url.Values{"title": {title}, "notes": {notes}, "occurred_at": {occurred}}.Encode()
}

func TestPageRoutes(t *testing.T) {
	s := &store{entry: work.Entry{ID: 12, Title: "Mentored a teammate", Notes: "A useful memory."}}
	h := site(t, s)
	for _, test := range []struct {
		path, text string
	}{
		{"/entries", "Start with something you want to remember"},
		{"/entries/new", "Record a memory"},
		{"/entries/12", "A useful memory."},
		{"/entries/12/edit", "Edit memory"},
		{"/entries/12/delete", "Delete this memory?"},
	} {
		t.Run(test.path, func(t *testing.T) {
			w := request(h, "GET", test.path, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), test.text) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if w.Header().Get("Content-Type") != "text/html; charset=utf-8" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("missing HTML/privacy headers: %v", w.Header())
			}
			if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || strings.Contains(w.Body.String(), "<script") {
				t.Fatal("pages should work without scripts")
			}
		})
	}
	if w := request(h, "GET", "/", ""); w.Code != 303 || w.Header().Get("Location") != "/entries" {
		t.Fatalf("root redirect: %d %v", w.Code, w.Header())
	}
	w := request(h, "GET", "/assets/style.css", "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") || !strings.Contains(w.Body.String(), ".memory-card") {
		t.Fatalf("embedded stylesheet: %d %s", w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/assets/assets.go", ""); w.Code != 404 {
		t.Fatal("source files must not be exposed")
	}
	for _, c := range s.calls {
		if c.owner != "owner" || (c.method != "get" && c.method != "list") {
			t.Fatalf("GET mutated data or used wrong owner: %+v", c)
		}
	}
}

func TestCreateAndUpdateForms(t *testing.T) {
	for _, method := range []string{"create", "update"} {
		t.Run(method, func(t *testing.T) {
			s := &store{entry: work.Entry{ID: 12}}
			h := site(t, s)
			path := "/entries"
			if method == "update" {
				path += "/12"
			}
			w := request(h, "POST", path, form("  Mentored a teammate  ", "No metrics needed.", "2026-01-02T17:00:00.123456"))
			if w.Code != 303 || w.Header().Get("Location") != "/entries/12" {
				t.Fatalf("save: %d %s", w.Code, w.Body.String())
			}
			last := s.calls[len(s.calls)-1]
			if last.method != method || last.owner != "owner" || (method == "update" && last.id != 12) {
				t.Fatalf("wrong persistence call: %+v", last)
			}
			if s.input.Title != "Mentored a teammate" || s.input.Notes != "No metrics needed." || s.input.OccurredAt == nil || s.input.OccurredAt.Location() != time.UTC || s.input.OccurredAt.Nanosecond() != 123456000 {
				t.Fatalf("bad input normalization: %+v", s.input)
			}
			w = request(h, "POST", path, form("Just a title", "", ""))
			if w.Code != 303 || s.input.OccurredAt != nil || s.input.Notes != "" {
				t.Fatalf("optional fields/replacement: %d %+v", w.Code, s.input)
			}
		})
	}
	// An HTML datetime-local control may submit minutes without seconds.
	s := &store{entry: work.Entry{ID: 12}}
	w := request(site(t, s), "POST", "/entries", form("Title", "", "2026-01-02T17:00"))
	if w.Code != 303 || s.input.OccurredAt == nil || s.input.OccurredAt.Hour() != 17 {
		t.Fatalf("minute precision: %d %+v", w.Code, s.input)
	}
}

func TestValidationPreservesSubmittedValues(t *testing.T) {
	for _, test := range []struct {
		name, title, notes, date string
	}{
		{"blank title", "   ", "Keep my notes", "2026-01-02T17:00"},
		{"long title", strings.Repeat("界", 201), "Keep my notes", ""},
		{"long notes", "Keep my title", strings.Repeat("a", 20001), ""},
		{"null character", "Keep my title", "invalid\x00notes", ""},
		{"invalid date", "Keep my title", "Keep my notes", "not-a-date"},
		{"invalid calendar date", "Keep my title", "Keep my notes", "2026-02-30T17:00"},
		{"year zero", "Keep my title", "Keep my notes", "0000-01-01T00:00"},
		{"leading newline", "Keep my title", "\nKeep the first blank line", "not-a-date"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, path := range []string{"/entries", "/entries/12"} {
				s := &store{entry: work.Entry{ID: 12}}
				w := request(site(t, s), "POST", path, form(test.title, test.notes, test.date))
				if w.Code != 400 || !strings.Contains(w.Body.String(), "We could not save this memory") {
					t.Fatalf("invalid form: %d %s", w.Code, w.Body.String())
				}
				if test.name != "null character" && (!strings.Contains(w.Body.String(), test.notes) || !strings.Contains(w.Body.String(), `value="`+test.title+`"`) || !strings.Contains(w.Body.String(), `value="`+test.date+`"`)) {
					t.Fatal("submitted fields were lost after validation failure")
				}
				// HTML strips the first newline after <textarea>. The template
				// supplies that newline, preserving any user-provided leading one.
				if test.name == "leading newline" && !strings.Contains(w.Body.String(), `aria-describedby="notes-hint">`+"\n"+test.notes+"</textarea>") {
					t.Fatal("browser parsing would strip the user's first newline")
				}
				for _, c := range s.calls {
					if c.method != "get" {
						t.Fatalf("invalid input reached write persistence: %+v", c)
					}
				}
			}
		})
	}
}

func TestEscapingAndEditTimePrecision(t *testing.T) {
	timestamp := time.Date(2026, 1, 2, 17, 0, 0, 123456000, time.UTC)
	s := &store{entry: work.Entry{ID: 12, Title: `<script>alert("title")</script>`, Notes: `</textarea><script>alert("notes")</script>`, OccurredAt: &timestamp}}
	s.entries = []work.Entry{s.entry}
	h := site(t, s)
	for _, path := range []string{"/entries", "/entries/12", "/entries/12/edit", "/entries/12/delete"} {
		w := request(h, "GET", path, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
			t.Fatalf("unsafe or failed template %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := request(h, "GET", "/entries/12/edit", "")
	if !strings.Contains(w.Body.String(), `value="2026-01-02T17:00:00.123"`) {
		t.Fatal("edit form must use browser-compatible millisecond precision")
	}
	w = request(h, "POST", "/entries/12", form("Edited title", "Notes", "2026-01-02T17:00:00.123"))
	if w.Code != 303 || s.input.OccurredAt == nil || !s.input.OccurredAt.Equal(timestamp) {
		t.Fatalf("unchanged form time lost original microseconds: %d %+v", w.Code, s.input)
	}
	w = request(h, "POST", "/entries/12", form("Edited title", "Notes", "2026-01-03T18:30"))
	if w.Code != 303 || s.input.OccurredAt == nil || s.input.OccurredAt.Day() != 3 || s.input.OccurredAt.Hour() != 18 {
		t.Fatal("an intentionally changed time must replace the original")
	}
	w = request(h, "POST", "/entries/12", form("Edited title", "Notes", ""))
	if w.Code != 303 || s.input.OccurredAt != nil {
		t.Fatal("clearing the occurrence field must remove the time")
	}
	s.saveErr = errors.New("sensitive database details")
	w = request(h, "POST", "/entries", form(s.entry.Title, s.entry.Notes, ""))
	if w.Code != 500 || strings.Contains(w.Body.String(), "<script>") || strings.Contains(w.Body.String(), "sensitive database details") || !strings.Contains(w.Body.String(), "Your text is still below") {
		t.Fatalf("unsafe save-error form: %d %s", w.Code, w.Body.String())
	}
}

func TestFormParsingBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"duplicate", "title=one&title=two", 400},
		{"unknown", "title=Title&extra=value", 400},
		{"client owner", "title=Title&owner_id=victim", 400},
		{"malformed", "title=%zz", 400},
		{"oversized", "title=" + strings.Repeat("a", 1<<20), 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &store{}
			w := request(site(t, s), "POST", "/entries", test.body)
			if w.Code != test.status || len(s.calls) != 0 {
				t.Fatalf("status=%d calls=%+v", w.Code, s.calls)
			}
		})
	}
	s := &store{entry: work.Entry{ID: 12}}
	w := request(site(t, s), "POST", "/entries?title=Wrong&owner_id=victim", "title=Right")
	if w.Code != 303 || s.input.Title != "Right" || s.calls[0].owner != "owner" {
		t.Fatalf("query overrode body/identity: %d %+v", w.Code, s)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/entries", strings.NewReader(`{"title":"Title"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	site(t, &store{}).ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatalf("website should require a form, got %d", w.Code)
	}
}

func TestPagination(t *testing.T) {
	s := &store{}
	for id := int64(100); id >= 80; id-- {
		s.entries = append(s.entries, work.Entry{ID: id, Title: fmt.Sprintf("Memory %d", id)})
	}
	h := site(t, s)
	w := request(h, "GET", "/entries", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `href="/entries?before_id=81"`) || strings.Contains(w.Body.String(), "Memory 80") {
		t.Fatalf("bad page: %d %s", w.Code, w.Body.String())
	}
	if s.calls[0].owner != "owner" || s.calls[0].limit != 21 {
		t.Fatalf("list must be bounded/owned: %+v", s.calls)
	}
	s.entries = nil
	w = request(h, "GET", "/entries?before_id=81", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No older memories") || !strings.Contains(w.Body.String(), "Newest memories") || s.calls[1].id != 81 {
		t.Fatalf("end of pagination: %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"before_id=0", "before_id=-1", "before_id=nope", "before_id=", "before_id=9223372036854775808", "before_id=1&before_id=2", "owner_id=victim", "limit=20", "before_id=%zz"} {
		before := len(s.calls)
		w = request(h, "GET", "/entries?"+query, "")
		if w.Code != 400 || len(s.calls) != before {
			t.Fatalf("bad cursor %s: %d", query, w.Code)
		}
	}
}

func TestDeleteRequiresPost(t *testing.T) {
	s := &store{entry: work.Entry{ID: 12, Title: "Keep this memory"}}
	h := site(t, s)
	w := request(h, "GET", "/entries/12/delete", "")
	if w.Code != 200 || len(s.calls) != 1 || s.calls[0].method != "get" || !strings.Contains(w.Body.String(), `method="post"`) || !strings.Contains(w.Body.String(), "cannot be undone") {
		t.Fatalf("confirmation must not delete: %d %+v", w.Code, s.calls)
	}
	w = request(h, "POST", "/entries/12/delete", "")
	if w.Code != 303 || w.Header().Get("Location") != "/entries" || s.calls[len(s.calls)-1] != (call{method: "delete", owner: "owner", id: 12}) {
		t.Fatalf("delete: %d %+v", w.Code, s.calls)
	}
}

func TestMissingAndInvalidEntries(t *testing.T) {
	for _, path := range []string{"/entries/12", "/entries/12/edit", "/entries/12/delete"} {
		s := &store{err: work.ErrNotFound}
		w := request(site(t, s), "GET", path, "")
		if w.Code != 404 || !strings.Contains(w.Body.String(), "could not be found") || s.calls[0].owner != "owner" {
			t.Fatalf("missing record: %d %s", w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/entries/12", "/entries/12/delete"} {
		s := &store{err: work.ErrNotFound}
		w := request(site(t, s), "POST", path, form("Title", "", ""))
		if w.Code != 404 || len(s.calls) != 1 || s.calls[0].method != "get" {
			t.Fatalf("missing write target: %d %+v", w.Code, s.calls)
		}
	}
	for _, id := range []string{"0", "-1", "bad", "9223372036854775808"} {
		s := &store{}
		w := request(site(t, s), "GET", "/entries/"+id, "")
		if w.Code != 400 || len(s.calls) != 0 {
			t.Fatalf("bad id: %d %+v", w.Code, s.calls)
		}
	}
	for _, path := range []string{"/entries", "/entries/12"} {
		w := request(site(t, &store{err: errors.New("sensitive SQL details")}), "GET", path, "")
		if w.Code != 500 || strings.Contains(w.Body.String(), "sensitive SQL details") {
			t.Fatalf("storage error: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestBrowserProtectionsAndAPIRouting(t *testing.T) {
	s := &store{}
	h := site(t, s)
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/entries", strings.NewReader("title=Title"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || len(s.calls) != 0 {
		t.Fatalf("cross-origin form reached persistence: %d %+v", w.Code, s.calls)
	}
	r.Header.Del("Origin")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || len(s.calls) != 0 {
		t.Fatal("cross-site form must be rejected")
	}
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"PATCH", "/api/entries/12", 405},
		{"GET", "/api/missing", 404},
		{"DELETE", "/entries/12", 405},
		{"GET", "/unknown", 404},
	} {
		if w := request(h, test.method, test.path, ""); w.Code != test.status {
			t.Fatalf("%s %s: got %d want %d", test.method, test.path, w.Code, test.status)
		}
	}
	w = request(h, "GET", "/api/entries", "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("JSON endpoint was shadowed: %d %s", w.Code, w.Body.String())
	}
}

func TestConcurrentRendering(t *testing.T) {
	h := site(t, &store{entry: work.Entry{ID: 12, Title: "Concurrent memory"}})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			w := request(h, "GET", "/entries/12", "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Concurrent memory") {
				t.Errorf("concurrent render: %d", w.Code)
			}
		})
	}
	wg.Wait()
}

func TestOwnerRequired(t *testing.T) {
	if _, err := web.NewHandler(&store{}, ""); err == nil {
		t.Fatal("a server-supplied owner is required")
	}
}
