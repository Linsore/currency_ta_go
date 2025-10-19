CREATE TABLE IF NOT EXISTS updates (
    id          UUID PRIMARY KEY,
    pair        TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('pending','done','error')),
    price       DOUBLE PRECISION,
    updated_at  TIMESTAMPTZ,
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_updates_status_created ON updates (status, created_at);

CREATE TABLE IF NOT EXISTS quotes (
    pair        TEXT PRIMARY KEY,
    price       DOUBLE PRECISION NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);