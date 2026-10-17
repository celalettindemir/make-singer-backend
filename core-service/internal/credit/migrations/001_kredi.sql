-- Kredi modulunun tablolari. Her ifade IF NOT EXISTS: API replikalari
-- start-first doner, ayni migration iki kez gorulebilir.
--
-- Kimligin kullanici tablosuna FK YOK: kimlik kullaniciyi once siler;
-- hesap silme kredi temizligini kendisi yapar (hesap silinir, abonelik ve
-- satin_alma anonimlestirilir, hareket silinir).

CREATE TABLE IF NOT EXISTS kredi.hesap (
    kullanici_id    uuid PRIMARY KEY,
    plan_bakiye     integer NOT NULL DEFAULT 0,
    kalici_bakiye   integer NOT NULL DEFAULT 0,
    plan_donem_sonu timestamptz NULL,
    magaza_token    uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    hediye_alindi   boolean NOT NULL DEFAULT false,
    guncellendi     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kredi.hareket (
    id            bigserial PRIMARY KEY,
    kullanici_id  uuid NOT NULL,
    tur           text NOT NULL
        CHECK (tur IN ('render', 'render_iade', 'soz', 'soz_iade', 'hediye',
                       'paket', 'plan_dolum', 'plan_bitis', 'magaza_iade')),
    ref           text NOT NULL,
    plan_delta    integer NOT NULL,
    kalici_delta  integer NOT NULL,
    sebep         text NOT NULL DEFAULT '',
    olusturuldu   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tur, ref)
);

CREATE INDEX IF NOT EXISTS hareket_kullanici_idx
    ON kredi.hareket (kullanici_id, id DESC);

CREATE TABLE IF NOT EXISTS kredi.eylem (
    ref           text PRIMARY KEY,
    kullanici_id  uuid NOT NULL,
    tur           text NOT NULL,
    kullanim      integer NOT NULL,
    olusturuldu   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kredi.abonelik (
    kullanici_id       uuid NULL,
    magaza             text NOT NULL,
    orijinal_islem_id  text NOT NULL,
    urun               text NOT NULL,
    durum              text NOT NULL
        CHECK (durum IN ('aktif', 'odeme_sorunu', 'ek_sure', 'bitti', 'iade')),
    donem_sonu         timestamptz NULL,
    otomatik_yenile    boolean NOT NULL DEFAULT true,
    sonraki_dolum      timestamptz NULL,
    dolum_capasi       timestamptz NULL,
    dolum_sayisi       integer NOT NULL DEFAULT 0,
    ortam              text NOT NULL,
    PRIMARY KEY (magaza, orijinal_islem_id)
);

CREATE INDEX IF NOT EXISTS abonelik_sonraki_dolum_idx
    ON kredi.abonelik (sonraki_dolum) WHERE durum = 'aktif';

CREATE TABLE IF NOT EXISTS kredi.satin_alma (
    magaza        text NOT NULL,
    islem_id      text NOT NULL,
    kullanici_id  uuid NULL,
    urun          text NOT NULL,
    kredi         integer NOT NULL,
    durum         text NOT NULL,
    ortam         text NOT NULL,
    ham_yuk       jsonb NULL,
    olusturuldu   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (magaza, islem_id)
);
