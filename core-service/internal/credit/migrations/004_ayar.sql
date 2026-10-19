-- Uygulama ayarlari (spec 7): tek satirlik anahtar/deger kayitlari.
-- Ilk anahtar `lyrics_provider`: yoneticinin sectigi soz yazari saglayicisi.
CREATE TABLE IF NOT EXISTS kredi.ayar (
    anahtar     text PRIMARY KEY,
    deger       text NOT NULL,
    degistiren  text NOT NULL,
    degisti     timestamptz NOT NULL DEFAULT now()
);
