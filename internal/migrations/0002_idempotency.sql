ALTER TABLE updates ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS ux_updates_pair_idem
  ON updates(pair, idempotency_key)
  WHERE idempotency_key IS NOT NULL;