# Clinical Trial Pipeline

A Go batch pipeline that ingests clinical trial records from three public registries, normalises them into a common schema, deduplicates overlapping records, and writes the result to PostgreSQL.

## Sources

| Source | Method |
|---|---|
| [WHO ICTRP](https://trialsearch.who.int/) | Scrapes the ASP.NET search UI (form post + pagination) |
| [EUCTR](https://www.clinicaltrialsregister.eu/) | Scrapes search result pages with `goquery` |
| [ISRCTN](https://www.isrctn.com/) | Queries the public XML API directly |

WHO ICTRP aggregates records from EUCTR and ISRCTN, so the same trial often appears under all three sources. The pipeline deduplicates these by trial ID, preferring the most source-specific record (EUCTR > ISRCTN > WHO).

## Architecture

```
ingest (parallel, one goroutine per source)
        │
        ▼
transform (normalise fields, dedupe across sources)
        │
        ▼
warehouse (upsert into Postgres)
```

See `CLAUDE.md` for a more detailed breakdown of each package.

## Requirements

- Go 1.25+
- PostgreSQL

## Setup

1. Apply the database schema:

   ```bash
   psql "$DATABASE_URL" -f sql/migration.sql
   ```

2. Configure `.env` (or real environment variables):

   ```env
   DATABASE_URL=postgres://user:password@localhost:5432/dbname?sslmode=disable
   WHO_QUERY=cancer
   EUCTR_QUERY=cancer
   ISRCTN_QUERY=cancer
   ```

3. Run the pipeline:

   ```bash
   go run ./cmd/pipeline
   ```

The pipeline fetches from all three sources concurrently, prints a summary of ingested trials to the terminal, and upserts them into the `trials` table.

## Project layout

```
cmd/pipeline/        entrypoint — orchestrates ingest → transform → warehouse
config/              .env loading and pipeline configuration
internal/ingester/   one file per registry source, implementing the Ingester interface
internal/models/     the normalised Trial record and field-normalisation helpers
internal/transformer/ cleaning and cross-source deduplication
internal/warehouse/  PostgreSQL persistence (pgx)
sql/migration.sql    database schema
```

## Development

```bash
go build ./...   # build
go vet ./...     # static checks
```
