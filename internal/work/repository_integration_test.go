package work_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/siegema/superego-api/internal/accounts"
	"github.com/siegema/superego-api/internal/database"
	"github.com/siegema/superego-api/internal/server"
	"github.com/siegema/superego-api/internal/work"
)

// Each run owns an isolated schema; never truncate or migrate the caller's schema.
func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("SUPEREGO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set SUPEREGO_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "superego_test_" + strings.ToLower(rand.Text())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("clean up integration schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	applyMigration(t, pool, "up")
	return pool
}

func applyMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	sql, err := os.ReadFile("../../migrations/0001_entries." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply %s migration: %v", direction, err)
	}
}

func TestPostgresEntryLifecycle(t *testing.T) {
	pool := integrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accountRepo := accounts.NewRepository(pool)
	// Startup must be safe to repeat against an existing development identity.
	for range 2 {
		if err := accountRepo.EnsureDevelopmentUser(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id) VALUES ('another-user')`); err != nil {
		t.Fatal(err)
	}
	repo := work.NewRepository(pool)
	owner := accounts.DevelopmentUserID
	handler := server.NewHandler(work.NewHandler(repo, owner))
	otherHandler := server.NewHandler(work.NewHandler(repo, "another-user"))

	w := request(handler, "POST", "/api/entries", `{"title":"  Fixed deployments  ","notes":"Teammates can deploy independently.","occurred_at":"2026-01-02T12:00:00-05:00"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var entry work.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.ID < 1 || entry.Title != "Fixed deployments" || entry.CreatedAt.IsZero() || entry.UpdatedAt.IsZero() || entry.OccurredAt == nil || entry.OccurredAt.UTC().Hour() != 17 {
		t.Fatalf("bad stored entry: %+v", entry)
	}
	if entry.CreatedAt.Location() != time.UTC || entry.UpdatedAt.Location() != time.UTC || entry.OccurredAt.Location() != time.UTC {
		t.Fatalf("timestamps must be returned in UTC: %+v", entry)
	}
	path := "/api/entries/" + strconv.FormatInt(entry.ID, 10)
	if w.Header().Get("Location") != path {
		t.Fatalf("bad Location: %s", w.Header().Get("Location"))
	}
	w = request(handler, "GET", path, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Teammates can deploy independently.") {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		w = request(otherHandler, method, path, `{"title":"Hijacked"}`)
		if w.Code != 404 {
			t.Fatalf("cross-owner %s: %d %s", method, w.Code, w.Body.String())
		}
	}
	w = request(otherHandler, "GET", "/api/entries", "")
	if strings.TrimSpace(w.Body.String()) != `{"entries":[],"next_before_id":null}` {
		t.Fatalf("cross-owner list leaked data: %s", w.Body.String())
	}

	w = request(handler, "PUT", path, `{"title":"Deployment improvements"}`)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	var updated work.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Deployment improvements" || updated.Notes != "" || updated.OccurredAt != nil || !updated.CreatedAt.Equal(entry.CreatedAt) || !updated.UpdatedAt.After(entry.UpdatedAt) {
		t.Fatalf("bad replacement: %+v", updated)
	}

	second, err := repo.CreateEntry(ctx, owner, work.EntryInput{Title: "Mentored a teammate"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEntry(ctx, "another-user", work.EntryInput{Title: "Private other entry"}); err != nil {
		t.Fatal(err)
	}
	w = request(handler, "GET", "/api/entries?limit=1", "")
	var page struct {
		Entries []work.Entry `json:"entries"`
		Next    *string      `json:"next_before_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(page.Entries) != 1 || page.Entries[0].ID != second.ID || page.Next == nil || *page.Next != strconv.FormatInt(second.ID, 10) {
		t.Fatalf("first page: %d %s", w.Code, w.Body.String())
	}
	w = request(handler, "GET", "/api/entries?limit=1&before_id="+*page.Next, "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(page.Entries) != 1 || page.Entries[0].ID != entry.ID || page.Next != nil {
		t.Fatalf("second page: %d %s", w.Code, w.Body.String())
	}

	// A fresh repository/pool reads persisted data, not process-local memory.
	config := pool.Config()
	freshPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(freshPool.Close)
	if persisted, err := work.NewRepository(freshPool).GetEntry(ctx, owner, entry.ID); err != nil || persisted.Title != updated.Title {
		t.Fatalf("persistence: %+v, %v", persisted, err)
	}

	w = request(handler, "DELETE", path, "")
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		w = request(handler, method, path, `{"title":"Missing"}`)
		if w.Code != 404 {
			t.Fatalf("missing %s: %d %s", method, w.Code, w.Body.String())
		}
	}
	if _, err := repo.CreateEntry(ctx, "nonexistent-user", work.EntryInput{Title: "Invalid ownership"}); err == nil {
		t.Fatal("owner foreign key was not enforced")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetEntry(ctx, owner, second.ID); !errors.Is(err, work.ErrNotFound) {
		t.Fatalf("user deletion did not cascade: %v", err)
	}
	applyMigration(t, pool, "down")
	applyMigration(t, pool, "up")
	if err := accountRepo.EnsureDevelopmentUser(ctx); err != nil {
		t.Fatalf("migration round trip: %v", err)
	}
}
