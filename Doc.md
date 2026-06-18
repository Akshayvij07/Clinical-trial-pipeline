# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go batch pipeline that scrapes/queries three public clinical trial registries (WHO ICTRP, EUCTR, ISRCTN), normalises the records into a common schema, deduplicates trials that appear in multiple registries, and upserts the result into PostgreSQL.

## Commands

```bash
# Run the pipeline (reads .env, requires DATABASE_URL pointing at a reachable Postgres instance)
go run ./cmd/pipeline

# Build
go build ./...

# Vet
go vet ./...

# Apply/refresh the DB schema
psql "$DATABASE_URL" -f sql/migration.sql
```

Note: `go test ./...` currently runs nothing — `internal/ingester/euctr_test.go` has no `func Test*` functions. It's a standalone scratch file with duplicated copies of the label-extraction helpers used to manually trace EUCTR parsing logic, not an executable test suite.

The `.git` directory exists but is empty (not an initialized repository) — `git` commands will fail with "not a git repository" until `git init` is run.

## Architecture

Pipeline shape (see `cmd/pipeline/main.go`): **ingest (parallel) → transform (clean + dedupe) → warehouse (upsert)**.

### Ingester layer (`internal/ingester`)

Every registry source implements the single-method `Ingester` interface (`ingester.go`):

```go
type Ingester interface {
    Name() string
    Fetch(ctx context.Context) ([]models.Trial, error)
}
```

`main.go` runs all configured ingesters concurrently (one goroutine each, results collected over a channel), so adding a new registry means writing a new file that implements this interface and adding it to the `ingesters` slice in `main.go` — no other wiring is needed.

Each source has a fundamentally different fetch strategy:
- **`who.go`** — scrapes `trialsearch.who.int`, an ASP.NET WebForms site. Requires simulating the browser flow: GET homepage → POST search with hidden `__VIEWSTATE`-style fields (`extractHiddenFields`) → POST to change page size → paginate via `__doPostBack('GridView1','Page$N')` events. HTML rows are parsed with `golang.org/x/net/html` directly in `who_html_parse.go` (no goquery here).
- **`euctr.go`** — scrapes `clinicaltrialsregister.eu` search result pages using `goquery` (CSS-selector style). Parsing falls back through three strategies (`table.result` → any `<tr>` → any EudraCT link) since the page layout isn't fully stable. Building the correct trial detail URL requires picking the right per-country ISO code (`nameToCode`/`codeToName` maps) because EUCTR detail pages 404 if you guess the wrong country suffix.
- **`isrtcn.go`** — calls the ISRCTN JSON/XML query API directly (no scraping/pagination, single request with a `limit`).
- **`whoformat.go`** — shared XML schema parser (`parseWHOFormatXML`) used by both the ISRCTN API response and the WHO ICTRP weekly XML export format, since both registries publish the same `<trials><trial>...` structure.
- **`helper.go`** — small shared string/HTML/date utilities used across ingesters.

### Models (`internal/models/model.go`)

`Trial` is the single normalised record type every ingester maps into, tagged with `db:"..."` for the warehouse layer. `NormaliseStatus` and `NormalisePhase` map each registry's free-text status/phase strings into a controlled vocabulary (`recruiting`, `completed`, `phase_1`, etc.) — ingesters call these directly when building a `Trial` rather than leaving raw text in those fields.

### Transformer (`internal/transformer/transformer.go`)

Two-step `Transform()`: trim/clean every field, drop trials with no ID/title, then deduplicate across sources by normalised ID (dashes stripped, lowercased) using a fixed priority **EUCTR > ISRCTN > WHO** — WHO ICTRP aggregates records from the other two registries, so the same trial commonly shows up three times and the most source-specific record wins.

### Warehouse (`internal/warehouse/postgres.go`)

Thin `pgx`/`pgxpool` wrapper. `UpsertTrials` batches writes (100 rows/batch) via `pgx.Batch`, `ON CONFLICT (id, source) DO UPDATE` — composite key is `(id, source)` since the same trial ID can legitimately exist under different sources before dedup, and `created_at`/`is_deleted` are intentionally never overwritten on conflict (`updated_at` is maintained by a DB trigger, see `sql/migration.sql`).

### Config (`config/config.go`)

Minimal hand-rolled `.env` loader (no external dependency) — reads `DATABASE_URL`, `WHO_QUERY`, `EUCTR_QUERY`, `ISRCTN_QUERY`, defaulting query terms to `"cancer"`. Real environment variables always take precedence over `.env` values.
