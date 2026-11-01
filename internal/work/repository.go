package work

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository stores work records using the application's shared connection pool.
// Every operation requires an owner; entry IDs alone never grant access.
type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const entryColumns = "id, title, notes, occurred_at, created_at, updated_at"

func scanEntry(row pgx.Row) (Entry, error) {
	var entry Entry
	err := row.Scan(&entry.ID, &entry.Title, &entry.Notes, &entry.OccurredAt, &entry.CreatedAt, &entry.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	// pgx scans timestamptz in the process's local zone by default. Keep the
	// API representation independent of the machine running the server.
	entry.CreatedAt = entry.CreatedAt.UTC()
	entry.UpdatedAt = entry.UpdatedAt.UTC()
	if entry.OccurredAt != nil {
		utc := entry.OccurredAt.UTC()
		entry.OccurredAt = &utc
	}
	return entry, nil
}

func (r *Repository) CreateEntry(ctx context.Context, owner string, input EntryInput) (Entry, error) {
	return scanEntry(r.pool.QueryRow(ctx, `
		INSERT INTO entries (owner_id, title, notes, occurred_at)
		VALUES ($1, $2, $3, $4) RETURNING `+entryColumns,
		owner, input.Title, input.Notes, input.OccurredAt))
}

func (r *Repository) GetEntry(ctx context.Context, owner string, id int64) (Entry, error) {
	return scanEntry(r.pool.QueryRow(ctx, `SELECT `+entryColumns+`
		FROM entries WHERE owner_id = $1 AND id = $2`, owner, id))
}

// ListEntries returns newest IDs first. A zero beforeID starts at the newest entry.
func (r *Repository) ListEntries(ctx context.Context, owner string, beforeID int64, limit int) ([]Entry, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+entryColumns+`
		FROM entries WHERE owner_id = $1 AND ($2::bigint = 0 OR id < $2)
		ORDER BY id DESC LIMIT $3`, owner, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (r *Repository) UpdateEntry(ctx context.Context, owner string, id int64, input EntryInput) (Entry, error) {
	return scanEntry(r.pool.QueryRow(ctx, `
		UPDATE entries SET title = $3, notes = $4, occurred_at = $5, updated_at = clock_timestamp()
		WHERE owner_id = $1 AND id = $2 RETURNING `+entryColumns,
		owner, id, input.Title, input.Notes, input.OccurredAt))
}

func (r *Repository) DeleteEntry(ctx context.Context, owner string, id int64) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM entries WHERE owner_id = $1 AND id = $2`, owner, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
