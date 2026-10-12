-- Kesfet (sosyal) modulunun TUM tablolari. 5b-5d'nin tablolari da burada
-- kurulur ki sonraki asamalar yeni migration'a ihtiyac duymadan uc
-- ekleyebilsin. Her ifade IF NOT EXISTS: API replikalari start-first
-- doner, ayni migration iki kez gorulebilir.
--
-- Kimligin kullanici tablosuna FK YOK: kimlik kullaniciyi once siler; bir cascade, R2
-- temizligi icin gereken yayin kimliklerini yok ederdi (bkz. HesapService).

CREATE TABLE IF NOT EXISTS sosyal.profil (
    kullanici_id  uuid PRIMARY KEY,
    kullanici_adi text NOT NULL UNIQUE
        CHECK (kullanici_adi ~ '^[a-z0-9_.]{3,20}$'
               AND left(kullanici_adi, 1) <> '.'
               AND right(kullanici_adi, 1) <> '.'),
    gorunen_ad    text NOT NULL CHECK (char_length(gorunen_ad) BETWEEN 1 AND 40),
    bio           text NOT NULL DEFAULT '' CHECK (char_length(bio) <= 160),
    olusturma     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sosyal.yayin (
    id                uuid PRIMARY KEY,
    slug              text NOT NULL UNIQUE CHECK (slug ~ '^[A-Za-z0-9]{8}$'),
    sahip_id          uuid NOT NULL,
    baslik            text NOT NULL CHECK (char_length(baslik) BETWEEN 1 AND 80),
    aciklama          text NOT NULL DEFAULT '' CHECK (char_length(aciklama) <= 300),
    tur               text NOT NULL,
    bpm               integer NOT NULL,
    ton_tonik         text NOT NULL,
    ton_mod           text NOT NULL CHECK (ton_mod IN ('major', 'minor')),
    sure_sn           integer NOT NULL CHECK (sure_sn > 0),
    gorunurluk        text NOT NULL CHECK (gorunurluk IN ('herkes', 'takipciler', 'link')),
    altyapi_acik      boolean NOT NULL,
    yorum_acik        boolean NOT NULL,
    sozler            jsonb NOT NULL,
    yapi              jsonb NOT NULL,
    dalga_formu       jsonb NOT NULL DEFAULT '[]',
    -- [{id, enstruman, sure, anahtar}]; anahtar disari verilmez.
    stem_listesi      jsonb NOT NULL DEFAULT '[]',
    -- Kaynak anahtarlar: {masterKey, stems:[{id, enstruman, sure, key}]}.
    -- tekrar-dene ayni kopyayi yeniden kurabilsin diye satirda durur.
    hazirlik          jsonb NOT NULL,
    master_anahtari   text,
    onizleme_anahtari text,
    -- Cover ise orijinal. FK YOK: orijinal silinince cover kalir ve
    -- kaynak_baslik / kaynak_sahip_adi son bilinen degerleri tasir.
    kaynak_yayin_id   uuid,
    kaynak_baslik     text,
    kaynak_sahip_adi  text,
    durum             text NOT NULL CHECK (durum IN ('hazirlaniyor', 'yayinda', 'hata')),
    olusturma         timestamptz NOT NULL DEFAULT now(),
    silinme           timestamptz
);

CREATE INDEX IF NOT EXISTS yayin_sahip_idx ON sosyal.yayin (sahip_id, olusturma DESC, id DESC);
CREATE INDEX IF NOT EXISTS yayin_akis_idx ON sosyal.yayin (olusturma DESC, id DESC)
    WHERE silinme IS NULL AND durum = 'yayinda';
CREATE INDEX IF NOT EXISTS yayin_kaynak_idx ON sosyal.yayin (kaynak_yayin_id)
    WHERE kaynak_yayin_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS sosyal.begeni (
    kullanici_id uuid NOT NULL,
    yayin_id     uuid NOT NULL REFERENCES sosyal.yayin (id) ON DELETE CASCADE,
    olusturma    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kullanici_id, yayin_id)
);
CREATE INDEX IF NOT EXISTS begeni_yayin_idx ON sosyal.begeni (yayin_id, olusturma);

CREATE TABLE IF NOT EXISTS sosyal.takip (
    takipci_id      uuid NOT NULL,
    takip_edilen_id uuid NOT NULL,
    olusturma       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (takipci_id, takip_edilen_id),
    CHECK (takipci_id <> takip_edilen_id)
);
CREATE INDEX IF NOT EXISTS takip_edilen_idx ON sosyal.takip (takip_edilen_id);

CREATE TABLE IF NOT EXISTS sosyal.yorum (
    id        uuid PRIMARY KEY,
    yayin_id  uuid NOT NULL REFERENCES sosyal.yayin (id) ON DELETE CASCADE,
    yazar_id  uuid NOT NULL,
    metin     text NOT NULL CHECK (char_length(metin) BETWEEN 1 AND 500),
    zaman_ms  integer CHECK (zaman_ms >= 0),
    olusturma timestamptz NOT NULL DEFAULT now(),
    silinme   timestamptz
);
CREATE INDEX IF NOT EXISTS yorum_yayin_idx ON sosyal.yorum (yayin_id, olusturma);
CREATE INDEX IF NOT EXISTS yorum_yazar_idx ON sosyal.yorum (yazar_id);

-- yayin_id: hedefin ait oldugu yayin (hedef yorumsa yorumun yayini).
-- Cok bicimli hedef_id'ye FK kurulamaz; cascade bu kolon uzerinden gelir.
CREATE TABLE IF NOT EXISTS sosyal.sikayet (
    id          uuid PRIMARY KEY,
    bildiren_id uuid NOT NULL,
    hedef_turu  text NOT NULL CHECK (hedef_turu IN ('yayin', 'yorum')),
    hedef_id    uuid NOT NULL,
    yayin_id    uuid NOT NULL REFERENCES sosyal.yayin (id) ON DELETE CASCADE,
    neden       text NOT NULL CHECK (neden IN ('spam', 'taciz', 'telif', 'uygunsuz', 'diger')),
    aciklama    text NOT NULL DEFAULT '' CHECK (char_length(aciklama) <= 300),
    olusturma   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (bildiren_id, hedef_turu, hedef_id)
);
