CREATE TABLE IF NOT EXISTS uretim.isler (
    id            uuid PRIMARY KEY,
    sahip         text NOT NULL,
    tur           text NOT NULL,
    durum         text NOT NULL,
    ilerleme      int NOT NULL DEFAULT 0,
    adim          text,
    proje_id      text NOT NULL DEFAULT '',
    proje_adi     text NOT NULL DEFAULT '',
    girdi         jsonb,
    sonuc         jsonb,
    hata          text,
    hata_kodu     text,
    deneme        int NOT NULL DEFAULT 0,
    son_seq       bigint NOT NULL DEFAULT 0,
    olusturma     timestamptz NOT NULL,
    baslama       timestamptz,
    bitis         timestamptz,
    son_kullanma  timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS isler_sahip_olusturma ON uretim.isler (sahip, olusturma DESC);
CREATE UNIQUE INDEX IF NOT EXISTS isler_sozler_aktif ON uretim.isler (sahip, proje_id)
    WHERE tur = 'lyrics' AND durum IN ('queued', 'running');
CREATE INDEX IF NOT EXISTS isler_son_kullanma ON uretim.isler (son_kullanma);

CREATE TABLE IF NOT EXISTS uretim.is_olaylari (
    is_id  uuid NOT NULL REFERENCES uretim.isler (id) ON DELETE CASCADE,
    seq    bigint NOT NULL,
    tur    text NOT NULL,
    veri   jsonb NOT NULL,
    zaman  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (is_id, seq)
);

CREATE TABLE IF NOT EXISTS uretim.cihazlar (
    token       text PRIMARY KEY,
    sahip       text NOT NULL,
    platform    text NOT NULL,
    dil         text NOT NULL,
    surum       text NOT NULL DEFAULT '',
    guncelleme  timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS cihazlar_sahip ON uretim.cihazlar (sahip);
