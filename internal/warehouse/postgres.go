package warehouse

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Akshayvij07/clinical-trial-pipeline/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgxpool connection pool and provides trial persistence.
type DB struct {
	pool *pgxpool.Pool
}

// New connects to PostgreSQL using the given connection string and returns a DB.
//
// connStr format: "postgres://user:password@localhost:5432/dbname?sslmode=disable"
func New(ctx context.Context, connStr string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}

	// Pool settings — reasonable defaults for a batch pipeline
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	// Verify connectivity
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	log.Printf("[warehouse] connected to PostgreSQL (%s)", cfg.ConnConfig.Host)
	return &DB{pool: pool}, nil
}

// Close releases all pool connections.
func (db *DB) Close() {
	db.pool.Close()
}

// UpsertTrials inserts or updates a batch of trials.
//
// Strategy: ON CONFLICT (id, source) DO UPDATE
// - Always updates mutable fields (title, status, phase, etc.)
// - Preserves created_at from the original insert
// - Sets updated_at to NOW() via the DB trigger
// - Never touches is_deleted (soft deletes are managed separately)
func (db *DB) UpsertTrials(ctx context.Context, trials []models.Trial) error {
	if len(trials) == 0 {
		return nil
	}

	// Use a batch for efficiency — one round trip per 100 trials
	const batchSize = 100
	total := 0

	for i := 0; i < len(trials); i += batchSize {
		end := i + batchSize
		if end > len(trials) {
			end = len(trials)
		}
		chunk := trials[i:end]

		n, err := db.upsertChunk(ctx, chunk)
		if err != nil {
			return fmt.Errorf("upsert chunk [%d:%d]: %w", i, end, err)
		}
		total += n
	}

	log.Printf("[warehouse] upserted %d/%d trials", total, len(trials))
	return nil
}

func (db *DB) upsertChunk(ctx context.Context, trials []models.Trial) (int, error) {
	// Build batch of COPY-style rows via pgx batch
	batch := &pgx.Batch{}

	const q = `
		INSERT INTO trials (
			id, source,
			title, status, phase, sponsor,
			start_date, end_date,
			conditions, interventions, countries,
			source_url, ingested_at
		) VALUES (
			$1,  $2,
			$3,  $4,  $5,  $6,
			$7,  $8,
			$9,  $10, $11,
			$12, $13
		)
		ON CONFLICT (id, source) DO UPDATE SET
			title          = EXCLUDED.title,
			status         = EXCLUDED.status,
			phase          = EXCLUDED.phase,
			sponsor        = EXCLUDED.sponsor,
			start_date     = EXCLUDED.start_date,
			end_date       = EXCLUDED.end_date,
			conditions     = EXCLUDED.conditions,
			interventions  = EXCLUDED.interventions,
			countries      = EXCLUDED.countries,
			source_url     = EXCLUDED.source_url,
			ingested_at    = EXCLUDED.ingested_at
			-- updated_at is handled by the DB trigger
			-- created_at and is_deleted are never overwritten
	`

	for _, t := range trials {
		batch.Queue(q,
			t.ID,
			t.Source,
			t.Title,
			t.Status,
			t.Phase,
			t.Sponsor,
			t.StartDate,
			t.EndDate,
			t.Conditions,
			t.Interventions,
			t.Countries,
			t.SourceURL,
			t.IngestedAt,
		)
	}

	results := db.pool.SendBatch(ctx, batch)
	defer results.Close()

	count := 0
	for range trials {
		if _, err := results.Exec(); err != nil {
			return count, fmt.Errorf("exec upsert: %w", err)
		}
		count++
	}

	return count, results.Close()
}

// GetTrial fetches a single trial by (id, source). Returns nil if not found.
func (db *DB) GetTrial(ctx context.Context, id, source string) (*models.Trial, error) {
	const q = `
		SELECT id, source, title, status, phase, sponsor,
		       start_date, end_date,
		       conditions, interventions, countries,
		       source_url, ingested_at
		FROM trials
		WHERE id = $1 AND source = $2 AND is_deleted = FALSE
	`

	row := db.pool.QueryRow(ctx, q, id, source)
	t := &models.Trial{}
	err := row.Scan(
		&t.ID, &t.Source,
		&t.Title, &t.Status, &t.Phase, &t.Sponsor,
		&t.StartDate, &t.EndDate,
		&t.Conditions, &t.Interventions, &t.Countries,
		&t.SourceURL, &t.IngestedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get trial: %w", err)
	}
	return t, nil
}

// CountTrials returns the total number of active (non-deleted) trials.
func (db *DB) CountTrials(ctx context.Context) (int, error) {
	var count int
	err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM trials WHERE is_deleted = FALSE`,
	).Scan(&count)
	return count, err
}

// CountBySource returns trial counts grouped by source.
func (db *DB) CountBySource(ctx context.Context) (map[string]int, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT source, COUNT(*) FROM trials WHERE is_deleted = FALSE GROUP BY source`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]int{}
	for rows.Next() {
		var src string
		var count int
		if err := rows.Scan(&src, &count); err != nil {
			return nil, err
		}
		result[src] = count
	}
	return result, rows.Err()
}
