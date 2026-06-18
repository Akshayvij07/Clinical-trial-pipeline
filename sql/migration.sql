-- ============================================================
--  Ceresity Clinical Trial Pipeline — Database Schema
-- ============================================================

CREATE TABLE IF NOT EXISTS trials (
    -- Identity (composite primary key: same trial ID can exist in multiple sources)
    id          TEXT        NOT NULL,
    source      TEXT        NOT NULL,

    -- Core metadata
    title           TEXT,
    status          TEXT,
    phase           TEXT,
    sponsor         TEXT,
    start_date      DATE,
    end_date        DATE,

    -- Classification arrays
    conditions      TEXT[]  DEFAULT '{}',
    interventions   TEXT[]  DEFAULT '{}',
    countries       TEXT[]  DEFAULT '{}',

    -- Provenance
    source_url      TEXT,
    ingested_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Audit
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    is_deleted      BOOLEAN     NOT NULL DEFAULT FALSE,

    PRIMARY KEY (id, source)
);

-- ── Indexes ───────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_trials_source     ON trials (source);
CREATE INDEX IF NOT EXISTS idx_trials_status     ON trials (status);
CREATE INDEX IF NOT EXISTS idx_trials_phase      ON trials (phase);
CREATE INDEX IF NOT EXISTS idx_trials_start_date ON trials (start_date);
CREATE INDEX IF NOT EXISTS idx_trials_is_deleted ON trials (is_deleted);

-- GIN indexes for array searches (e.g. WHERE 'Cancer' = ANY(conditions))
CREATE INDEX IF NOT EXISTS idx_trials_conditions    ON trials USING GIN (conditions);
CREATE INDEX IF NOT EXISTS idx_trials_countries     ON trials USING GIN (countries);
CREATE INDEX IF NOT EXISTS idx_trials_interventions ON trials USING GIN (interventions);

-- ── Auto-update updated_at ────────────────────────────────────
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_trials_updated_at ON trials;
CREATE TRIGGER trg_trials_updated_at
    BEFORE UPDATE ON trials
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();