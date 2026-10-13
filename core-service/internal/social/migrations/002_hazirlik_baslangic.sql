-- When the publication entered the preparing status. The stale sweep
-- (sosyal:bayat-tara) uses it to fail publications whose prepare job was lost.
-- Additive and idempotent: existing rows get now() (the migration time) as a
-- grace period, so nothing is swept on the first run.
ALTER TABLE sosyal.yayin
    ADD COLUMN IF NOT EXISTS hazirlik_baslangic timestamptz NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS yayin_hazirlik_baslangic_idx
    ON sosyal.yayin (hazirlik_baslangic) WHERE durum = 'hazirlaniyor';
