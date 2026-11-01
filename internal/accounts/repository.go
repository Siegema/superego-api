package accounts

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// EnsureDevelopmentUser creates the local-only identity without seeding it into
// the schema migration. This is not an authentication or registration mechanism.
func (r *Repository) EnsureDevelopmentUser(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO users (id) VALUES ($1) ON CONFLICT (id) DO NOTHING`, DevelopmentUserID)
	return err
}
