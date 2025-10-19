// internal/repo/repo.go
//
// Package repo contains the data-access layer for the service.
// It abstracts PostgreSQL interactions for updates and quotes, and
// provides a tiny migration runner that executes SQL files embedded
// at build time (see /migrations).
//
// Responsibilities:
//  - RunMigrations: apply SQL migrations on startup
//  - CRUD-style helpers for `updates` and `quotes` tables
//  - No business logic: only data persistence and retrieval

package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"currency_ta_go/internal/migrations"
)

// Embeds all SQL files from the migrations directory at compile time.


// Repo is a thin wrapper around a pgx connection pool used by the
// service to persist and query updates and quotes.
type Repo struct {
	db *pgxpool.Pool
}

// New creates a new repository backed by the provided pgx pool.
func New(pool *pgxpool.Pool) *Repo {
	return &Repo{db: pool}
}

// RunMigrations executes embedded SQL migration files in-order.
// It is intentionally simple: if a migration fails, startup should fail.
// Note: For production, consider a real migration tool (e.g., goose, migrate)
// for versioning and transactional safety across multiple files.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	files := []string{
		"0001_init.sql",
		"0002_idempotency.sql",
	}

	for _, path := range files {
		sqlBytes, readErr := migrations.Files.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", path, readErr)
		}
		if _, execErr := pool.Exec(ctx, string(sqlBytes)); execErr != nil {
			return fmt.Errorf("execute migration %s: %w", path, execErr)
		}
	}

	return nil
}

// ---- Row types (scan targets) ----

// UpdateRow represents a single record from the `updates` table.
type UpdateRow struct {
	ID        uuid.UUID
	Pair      string
	Status    string
	Price     sql.NullFloat64
	UpdatedAt sql.NullTime
	Error     sql.NullString
	Idem      sql.NullString // idempotency_key
}

// QuoteRow represents a single record from the `quotes` table.
type QuoteRow struct {
	Pair      string
	Price     float64
	UpdatedAt time.Time
}

// ---- Queries: updates ----

// InsertUpdate creates a new "pending" update job for the given pair.
// If idem (idempotency key) is provided and the unique index is enforced,
// the DB will reject duplicates; the caller should resolve races if needed.
func (r *Repo) InsertUpdate(
	ctx context.Context,
	updateID uuid.UUID,
	pair string,
	idempotencyKey *string,
) error {
	_, err := r.db.Exec(
		ctx,
		`INSERT INTO updates(id, pair, status, idempotency_key)
         VALUES ($1, $2, 'pending', $3)`,
		updateID, pair, idempotencyKey,
	)
	return err
}

// FindUpdateByIdem returns the update ID previously created with the same
// pair and idempotency key. Use this to make POST /quotes/update idempotent.
func (r *Repo) FindUpdateByIdem(
	ctx context.Context,
	pair string,
	idempotencyKey string,
) (uuid.UUID, error) {
	var updateID uuid.UUID
	err := r.db.QueryRow(
		ctx,
		`SELECT id FROM updates WHERE pair=$1 AND idempotency_key=$2`,
		pair, idempotencyKey,
	).Scan(&updateID)

	return updateID, err
}

// GetUpdate fetches an update row by its UUID. Returns sql.ErrNoRows if not found.
func (r *Repo) GetUpdate(ctx context.Context, updateID uuid.UUID) (UpdateRow, error) {
	var row UpdateRow
	err := r.db.QueryRow(
		ctx,
		`SELECT id, pair, status, price, updated_at, error, idempotency_key
         FROM updates
         WHERE id=$1`,
		updateID,
	).Scan(&row.ID, &row.Pair, &row.Status, &row.Price, &row.UpdatedAt, &row.Error, &row.Idem)

	return row, err
}

// SetUpdateDoneAndUpsertQuote finalizes the update with a price and timestamp,
// and upserts the latest quote for the pair in a single transaction.
// If any statement fails, no changes are committed.
func (r *Repo) SetUpdateDoneAndUpsertQuote(
	ctx context.Context,
	updateID uuid.UUID,
	pair string,
	price float64,
	updatedAt time.Time,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // safe to call even after Commit

	if _, err := tx.Exec(
		ctx,
		`UPDATE updates
         SET status='done', price=$2, updated_at=$3, error=NULL
         WHERE id=$1`,
		updateID, price, updatedAt,
	); err != nil {
		return err
	}

	if _, err := tx.Exec(
		ctx,
		`INSERT INTO quotes(pair, price, updated_at)
         VALUES ($1, $2, $3)
         ON CONFLICT (pair)
         DO UPDATE SET price=EXCLUDED.price, updated_at=EXCLUDED.updated_at`,
		pair, price, updatedAt,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// SetUpdateError marks the update as failed with an error message and timestamp.
func (r *Repo) SetUpdateError(
	ctx context.Context,
	updateID uuid.UUID,
	errorText string,
	updatedAt time.Time,
) error {
	_, err := r.db.Exec(
		ctx,
		`UPDATE updates
         SET status='error', error=$2, updated_at=$3
         WHERE id=$1`,
		updateID, errorText, updatedAt,
	)
	return err
}

// EnqueuePending returns up to `limit` update jobs with status 'pending'.
// The service worker can then push them into the in-memory channel.
func (r *Repo) EnqueuePending(
	ctx context.Context,
	limit int,
) ([]UpdateRow, error) {
	rows, err := r.db.Query(
		ctx,
		`SELECT id, pair
         FROM updates
         WHERE status='pending'
         ORDER BY created_at
         LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pending []UpdateRow
	for rows.Next() {
		var row UpdateRow
		if scanErr := rows.Scan(&row.ID, &row.Pair); scanErr != nil {
			return nil, scanErr
		}
		pending = append(pending, row)
	}
	return pending, rows.Err()
}

// ---- Queries: quotes ----

// GetQuote returns the last persisted quote for the given pair.
// Returns sql.ErrNoRows if the pair has never been updated.
func (r *Repo) GetQuote(ctx context.Context, pair string) (QuoteRow, error) {
	var row QuoteRow
	err := r.db.QueryRow(
		ctx,
		`SELECT pair, price, updated_at
         FROM quotes
         WHERE pair=$1`,
		pair,
	).Scan(&row.Pair, &row.Price, &row.UpdatedAt)

	return row, err
}

// ErrNotFound is kept for compatibility with service layer code that
// might choose to map repository-level "not found" semantics to HTTP 404.
// In most paths we rely on sql.ErrNoRows directly; this sentinel can be
// returned by higher layers when appropriate.
var ErrNotFound = errors.New("not found")
