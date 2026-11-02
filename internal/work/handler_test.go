package work_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/siegema/superego-api/internal/server"
	"github.com/siegema/superego-api/internal/work"
)

type storeCall struct {
	operation string
	owner     string
	id        int64
	limit     int
}

type stubStore struct {
	calls   []storeCall
	input   work.EntryInput
	entry   work.Entry
	entries []work.Entry
	err     error
}

func (s *stubStore) CreateEntry(_ context.Context, owner string, input work.EntryInput) (work.Entry, error) {
	s.calls = append(s.calls, storeCall{operation: "create", owner: owner})
	s.input = input
	return s.entry, s.err
}

func (s *stubStore) GetEntry(_ context.Context, owner string, id int64) (work.Entry, error) {
	s.calls = append(s.calls, storeCall{operation: "get", owner: owner, id: id})
	return s.entry, s.err
}

func (s *stubStore) ListEntries(_ context.Context, owner string, before int64, limit int) ([]work.Entry, error) {
	s.calls = append(s.calls, storeCall{operation: "list", owner: owner, id: before, limit: limit})
	return s.entries, s.err
}

func (s *stubStore) UpdateEntry(_ context.Context, owner string, id int64, input work.EntryInput) (work.Entry, error) {
	s.calls = append(s.calls, storeCall{operation: "update", owner: owner, id: id})
	s.input = input
	return s.entry, s.err
}

func (s *stubStore) DeleteEntry(_ context.Context, owner string, id int64) error {
	s.calls = append(s.calls, storeCall{operation: "delete", owner: owner, id: id})
	return s.err
}

func request(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestEntryOperations(t *testing.T) {
	for _, test := range []struct {
		method, path, body string
		status             int
		call               storeCall
	}{
		{"POST", "/api/entries", `{"title":"  Fixed deployments  "}`, 201, storeCall{operation: "create", owner: "owner"}},
		{"GET", "/api/entries/12", "", 200, storeCall{operation: "get", owner: "owner", id: 12}},
		{"PUT", "/api/entries/12", `{"title":"Fixed deployments"}`, 200, storeCall{operation: "update", owner: "owner", id: 12}},
		{"DELETE", "/api/entries/12", "", 204, storeCall{operation: "delete", owner: "owner", id: 12}},
		{"GET", "/api/entries", "", 200, storeCall{operation: "list", owner: "owner", limit: 51}},
		{"GET", "/api/entries?limit=2&before_id=20", "", 200, storeCall{operation: "list", owner: "owner", id: 20, limit: 3}},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			store := &stubStore{entry: work.Entry{ID: 12}}
			handler := server.NewHandler(work.NewHandler(store, "owner"), nil)
			w := request(handler, test.method, test.path, test.body)
			if w.Code != test.status {
				t.Fatalf("status %d, want %d: %s", w.Code, test.status, w.Body.String())
			}
			if !reflect.DeepEqual(store.calls, []storeCall{test.call}) {
				t.Fatalf("calls = %+v, want %+v", store.calls, test.call)
			}
			if test.method == "POST" || test.method == "PUT" {
				if store.input.Title != "Fixed deployments" {
					t.Fatalf("title was not normalized: %q", store.input.Title)
				}
			}
			if test.method == "POST" && w.Header().Get("Location") != "/api/entries/12" {
				t.Fatal("missing created entry location")
			}
			if test.method == "DELETE" && w.Body.Len() != 0 {
				t.Fatal("DELETE response should be empty")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private responses must not be cached")
			}
		})
	}
}

func TestEntryValidation(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"empty body", "", 400},
		{"null", "null", 400},
		{"array", "[]", 400},
		{"missing title", `{}`, 400},
		{"blank title", `{"title":" \n\t "}`, 400},
		{"long title", `{"title":"` + strings.Repeat("界", 201) + `"}`, 400},
		{"long notes", `{"title":"Title","notes":"` + strings.Repeat("a", 20001) + `"}`, 400},
		{"malformed", `{"title":`, 400},
		{"number title", `{"title":123}`, 400},
		{"unknown field", `{"title":"Title","extra":true}`, 400},
		{"client owner", `{"title":"Title","owner_id":"victim"}`, 400},
		{"multiple values", `{"title":"Title"} {}`, 400},
		{"trailing junk", `{"title":"Title"} junk`, 400},
		{"invalid date", `{"title":"Title","occurred_at":"yesterday"}`, 400},
		{"year zero", `{"title":"Title","occurred_at":"0000-01-01T00:00:00Z"}`, 400},
		{"null character", `{"title":"Title","notes":"\u0000"}`, 400},
		{"oversized", `{"title":"` + strings.Repeat("a", 1<<20) + `"}`, 413},
		{"oversized trailing whitespace", `{"title":"Title"}` + strings.Repeat(" ", 1<<20), 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, method := range []string{"POST", "PUT"} {
				store := &stubStore{}
				handler := server.NewHandler(work.NewHandler(store, "owner"), nil)
				path := "/api/entries"
				if method == "PUT" {
					path += "/1"
				}
				w := request(handler, method, path, test.body)
				if w.Code != test.status || len(store.calls) != 0 {
					t.Fatalf("%s: status=%d calls=%v body=%s", method, w.Code, store.calls, w.Body.String())
				}
			}
		})
	}
}

func TestOptionalEntryFields(t *testing.T) {
	store := &stubStore{entry: work.Entry{ID: 1}}
	handler := server.NewHandler(work.NewHandler(store, "owner"), nil)
	w := request(handler, "POST", "/api/entries", `{"title":"`+strings.Repeat("界", 200)+`","occurred_at":"2026-01-02T12:00:00-05:00"}`)
	if w.Code != 201 || store.input.Notes != "" {
		t.Fatalf("optional notes and Unicode title: %d %s", w.Code, w.Body.String())
	}
	if store.input.OccurredAt == nil || store.input.OccurredAt.Location() != time.UTC || store.input.OccurredAt.Hour() != 17 {
		t.Fatalf("occurrence time not normalized: %v", store.input.OccurredAt)
	}
}

func TestInvalidIDsAndPagination(t *testing.T) {
	paths := []string{
		"/api/entries/0", "/api/entries/-1", "/api/entries/nope", "/api/entries/9223372036854775808",
		"/api/entries?limit=0", "/api/entries?limit=101", "/api/entries?limit=nope", "/api/entries?limit=",
		"/api/entries?before_id=0", "/api/entries?before_id=-1", "/api/entries?before_id=nope",
		"/api/entries?before_id=9223372036854775808", "/api/entries?owner_id=victim",
		"/api/entries?limit=1&limit=2", "/api/entries?before_id=1&before_id=2",
		"/api/entries?limit=%zz", "/api/entries?limit=1;before_id=2",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			store := &stubStore{}
			w := request(server.NewHandler(work.NewHandler(store, "owner"), nil), "GET", path, "")
			if w.Code != 400 || len(store.calls) != 0 {
				t.Fatalf("status=%d calls=%v", w.Code, store.calls)
			}
		})
	}
	for _, method := range []string{"PUT", "DELETE"} {
		store := &stubStore{}
		w := request(server.NewHandler(work.NewHandler(store, "owner"), nil), method, "/api/entries/nope", `{"title":"Title"}`)
		if w.Code != 400 || len(store.calls) != 0 {
			t.Fatalf("%s invalid id: status=%d calls=%v", method, w.Code, store.calls)
		}
	}
}

func TestPaginationResponse(t *testing.T) {
	store := &stubStore{entries: []work.Entry{{ID: 3}, {ID: 2}, {ID: 1}}}
	handler := server.NewHandler(work.NewHandler(store, "owner"), nil)
	w := request(handler, "GET", "/api/entries?limit=2", "")
	var page struct {
		Entries []work.Entry `json:"entries"`
		Next    *string      `json:"next_before_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Next == nil || *page.Next != "2" {
		t.Fatalf("unexpected page: %+v", page)
	}
	store.entries = nil
	w = request(handler, "GET", "/api/entries", "")
	if strings.TrimSpace(w.Body.String()) != `{"entries":[],"next_before_id":null}` {
		t.Fatalf("unexpected empty page: %s", w.Body.String())
	}
}

func TestRepositoryErrors(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{work.ErrNotFound, 404},
		{errors.New("sensitive SQL connection details"), 500},
	} {
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			store := &stubStore{err: test.err}
			w := request(server.NewHandler(work.NewHandler(store, "owner"), nil), method, "/api/entries/1", `{"title":"Title"}`)
			if w.Code != test.status || strings.Contains(w.Body.String(), "sensitive") {
				t.Fatalf("%s: status=%d body=%s", method, w.Code, w.Body.String())
			}
		}
	}
	for _, method := range []string{"POST", "GET"} {
		store := &stubStore{err: errors.New("sensitive SQL details")}
		w := request(server.NewHandler(work.NewHandler(store, "owner"), nil), method, "/api/entries", `{"title":"Title"}`)
		if w.Code != 500 || strings.Contains(w.Body.String(), "sensitive") {
			t.Fatalf("%s: status=%d body=%s", method, w.Code, w.Body.String())
		}
	}
}

func TestLocalServerProtections(t *testing.T) {
	for _, test := range []struct {
		name, host, contentType, origin string
		status                          int
	}{
		{"wrong host", "attacker.example:8080", "application/json", "", 403},
		{"cross origin", "127.0.0.1:8080", "application/json", "https://attacker.example", 403},
		{"missing content type", "localhost:8080", "", "", 415},
		{"text content type", "localhost:8080", "text/plain", "", 415},
		{"valid json charset", "localhost:8080", "application/json; charset=utf-8", "", 201},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &stubStore{entry: work.Entry{ID: 1}}
			r := httptest.NewRequest("POST", "http://"+test.host+"/api/entries", strings.NewReader(`{"title":"Title"}`))
			r.Header.Set("Content-Type", test.contentType)
			r.Header.Set("Origin", test.origin)
			w := httptest.NewRecorder()
			server.NewHandler(work.NewHandler(store, "owner"), nil).ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if w.Code != 201 && len(store.calls) != 0 {
				t.Fatal("rejected request reached persistence")
			}
		})
	}
}

func TestRouting(t *testing.T) {
	store := &stubStore{}
	handler := server.NewHandler(work.NewHandler(store, "owner"), nil)
	if w := request(handler, "PATCH", "/api/entries/1", "{}"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PATCH returned %d", w.Code)
	}
	if w := request(handler, "GET", "/unknown", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown route returned %d", w.Code)
	}
	if len(store.calls) != 0 {
		t.Fatal("unrouted request reached persistence")
	}
}
