package work_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/siegema/superego-api/internal/accounts"
	"github.com/siegema/superego-api/internal/server"
	"github.com/siegema/superego-api/internal/web"
	"github.com/siegema/superego-api/internal/work"
)

func submitForm(handler http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080"+path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestPostgresWebsiteLifecycle(t *testing.T) {
	pool := integrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := accounts.NewRepository(pool).EnsureDevelopmentUser(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id) VALUES ('another-user')`); err != nil {
		t.Fatal(err)
	}
	repo := work.NewRepository(pool)
	owner := accounts.DevelopmentUserID
	pages, err := web.NewHandler(repo, owner)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.NewHandler(work.NewHandler(repo, owner), pages)
	otherPages, err := web.NewHandler(repo, "another-user")
	if err != nil {
		t.Fatal(err)
	}
	otherHandler := server.NewHandler(work.NewHandler(repo, "another-user"), otherPages)
	values := url.Values{
		"title":       {"  Investigated deployment failures  "},
		"notes":       {"A memory without metrics.\n<script>Not executable</script>"},
		"occurred_at": {"2026-01-02T17:00:00.123456"},
	}
	w := submitForm(handler, "/entries", values)
	if w.Code != 303 {
		t.Fatalf("create form: %d %s", w.Code, w.Body.String())
	}
	path := w.Header().Get("Location")
	id, err := strconv.ParseInt(strings.TrimPrefix(path, "/entries/"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetEntry(ctx, owner, id)
	if err != nil || stored.Title != "Investigated deployment failures" || stored.Notes != values.Get("notes") || stored.OccurredAt == nil || stored.OccurredAt.Nanosecond() != 123456000 {
		t.Fatalf("website did not persist input: %+v %v", stored, err)
	}
	for _, route := range []string{"/entries", path, path + "/edit"} {
		w = request(handler, "GET", route, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
			t.Fatalf("render %s: %d %s", route, w.Code, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), `value="2026-01-02T17:00:00.123"`) {
		t.Fatal("edit form must use browser-compatible time precision")
	}
	// JSON and HTML are two interfaces to the same persisted entry.
	w = request(handler, "GET", "/api/entries/"+strconv.FormatInt(id, 10), "")
	var fromAPI work.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &fromAPI); err != nil || w.Code != 200 || fromAPI.Title != stored.Title {
		t.Fatalf("API cannot read website entry: %d %v", w.Code, err)
	}
	w = request(handler, "POST", "/api/entries", `{"title":"Created through the JSON API"}`)
	if w.Code != 201 {
		t.Fatalf("API create: %d %s", w.Code, w.Body.String())
	}
	w = request(handler, "GET", "/entries", "")
	if !strings.Contains(w.Body.String(), "Created through the JSON API") {
		t.Fatal("website cannot read API entry")
	}

	values.Set("title", " ")
	values.Set("notes", "Keep my unsaved notes")
	w = submitForm(handler, path, values)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "Keep my unsaved notes") {
		t.Fatalf("failed validation lost input: %d %s", w.Code, w.Body.String())
	}
	if after, err := repo.GetEntry(ctx, owner, id); err != nil || after.Title != stored.Title || after.Notes != stored.Notes {
		t.Fatalf("invalid form altered saved data: %+v %v", after, err)
	}
	values.Set("title", "Deployment lessons")
	// Simulate the millisecond precision submitted by the native browser control.
	values.Set("occurred_at", "2026-01-02T17:00:00.123")
	w = submitForm(handler, path, values)
	if w.Code != 303 || w.Header().Get("Location") != path {
		t.Fatalf("update form: %d %s", w.Code, w.Body.String())
	}
	updated, err := repo.GetEntry(ctx, owner, id)
	if err != nil || updated.Title != "Deployment lessons" || !updated.CreatedAt.Equal(stored.CreatedAt) || !updated.UpdatedAt.After(stored.UpdatedAt) || !updated.OccurredAt.Equal(*stored.OccurredAt) {
		t.Fatalf("update persistence: %+v %v", updated, err)
	}
	for _, route := range []string{path, path + "/edit", path + "/delete"} {
		w = request(otherHandler, "GET", route, "")
		if w.Code != 404 || strings.Contains(w.Body.String(), "Deployment lessons") {
			t.Fatalf("cross-owner page leaked data: %d %s", w.Code, w.Body.String())
		}
	}
	for _, route := range []string{path, path + "/delete"} {
		w = submitForm(otherHandler, route, values)
		if w.Code != 404 {
			t.Fatalf("cross-owner form accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w = request(otherHandler, "GET", "/entries", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "Deployment lessons") {
		t.Fatal("cross-owner list leaked data")
	}
	w = request(handler, "GET", path+"/delete", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Delete this memory?") {
		t.Fatalf("confirmation: %d %s", w.Code, w.Body.String())
	}
	if _, err := repo.GetEntry(ctx, owner, id); err != nil {
		t.Fatalf("confirmation GET deleted data: %v", err)
	}
	w = submitForm(handler, path+"/delete", nil)
	if w.Code != 303 || w.Header().Get("Location") != "/entries" {
		t.Fatalf("delete form: %d %s", w.Code, w.Body.String())
	}
	w = request(handler, "GET", "/api/entries/"+strconv.FormatInt(id, 10), "")
	if w.Code != 404 {
		t.Fatalf("deleted website entry remains in API: %d", w.Code)
	}
}
