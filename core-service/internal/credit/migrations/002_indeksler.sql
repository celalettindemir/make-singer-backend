-- Additive indexes for per-user lookups and the action sweep.
CREATE INDEX IF NOT EXISTS abonelik_kullanici_idx
    ON kredi.abonelik (kullanici_id);

CREATE INDEX IF NOT EXISTS satin_alma_kullanici_idx
    ON kredi.satin_alma (kullanici_id);

CREATE INDEX IF NOT EXISTS eylem_olusturuldu_idx
    ON kredi.eylem (olusturuldu);
