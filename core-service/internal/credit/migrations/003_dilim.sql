-- Kredi v2: sureli kredi dilimleri, borc, kampanyalar, yonetici kolonlari.
-- 6a canliya cikmadi: veri tasinmaz, v1 kova kolonlari dogrudan kalkar
-- (6v2-G14). 001/002 degismez.
--
-- Bakiye hicbir yerde saklanmaz: suresi gecmemis dilimlerin kalan toplami
-- eksi kredi.hesap.borc.

-- Dilimler: her kredi girisi bir dilim. bitis NULL = suresiz.
CREATE TABLE IF NOT EXISTS kredi.dilim (
    id            bigserial PRIMARY KEY,
    kullanici_id  uuid NOT NULL,
    kaynak        text NOT NULL
        CHECK (kaynak IN ('plan', 'kampanya', 'paket', 'yonetici')),
    ref           text NOT NULL,
    ilk_miktar    integer NOT NULL CHECK (ilk_miktar > 0),
    kalan         integer NOT NULL CHECK (kalan >= 0 AND kalan <= ilk_miktar),
    bitis         timestamptz NULL,
    sure_islendi  boolean NOT NULL DEFAULT false,
    olusturuldu   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kaynak, ref)
);

CREATE INDEX IF NOT EXISTS dilim_kullanici_bitis_idx
    ON kredi.dilim (kullanici_id, bitis);

-- Sure taramasinin bekleyen dilimleri.
CREATE INDEX IF NOT EXISTS dilim_sure_idx
    ON kredi.dilim (bitis) WHERE NOT sure_islendi AND bitis IS NOT NULL;

-- Bir hareketin hangi dilimin kalanini ne kadar degistirdigi.
CREATE TABLE IF NOT EXISTS kredi.dilim_hareket (
    hareket_id  bigint NOT NULL,
    dilim_id    bigint NOT NULL,
    delta       integer NOT NULL,
    PRIMARY KEY (hareket_id, dilim_id)
);

-- Hesap: kovalar gider, borc gelir.
ALTER TABLE kredi.hesap
    DROP COLUMN IF EXISTS plan_bakiye,
    DROP COLUMN IF EXISTS kalici_bakiye,
    DROP COLUMN IF EXISTS plan_donem_sonu,
    DROP COLUMN IF EXISTS hediye_alindi;

ALTER TABLE kredi.hesap
    ADD COLUMN IF NOT EXISTS borc integer NOT NULL DEFAULT 0 CHECK (borc >= 0);

-- Hareket: tek imzali tutar, yonetici ve kampanya bilgisi.
ALTER TABLE kredi.hareket
    ADD COLUMN IF NOT EXISTS tutar integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS yapan text NULL,
    ADD COLUMN IF NOT EXISTS notu text NULL,
    ADD COLUMN IF NOT EXISTS kampanya_id bigint NULL,
    ADD COLUMN IF NOT EXISTS proje_id uuid NULL,
    ADD COLUMN IF NOT EXISTS kademe text NULL CHECK (kademe IN ('plus', 'pro'));

-- Gelistirme veritabanlarindaki v1 satirlari: tutar kova deltalarinin
-- toplami olur; v2'de karsiligi olmayan hosgeldin hediyesi silinir.
UPDATE kredi.hareket SET tutar = plan_delta + kalici_delta;
DELETE FROM kredi.hareket WHERE tur = 'hediye';

ALTER TABLE kredi.hareket
    DROP COLUMN IF EXISTS plan_delta,
    DROP COLUMN IF EXISTS kalici_delta;

ALTER TABLE kredi.hareket DROP CONSTRAINT IF EXISTS hareket_tur_check;
ALTER TABLE kredi.hareket ADD CONSTRAINT hareket_tur_check
    CHECK (tur IN ('render', 'render_iade', 'soz', 'soz_iade', 'master', 'master_iade',
                   'kampanya', 'paket', 'plan_dolum', 'plan_bitis', 'plan_sure',
                   'kampanya_sure', 'yonetici', 'yonetici_sure', 'magaza_iade'));

CREATE INDEX IF NOT EXISTS hareket_olusturuldu_idx
    ON kredi.hareket (olusturuldu DESC, id DESC);

-- Abonelik: dort urun, bekleyen kademe degisikligi.
ALTER TABLE kredi.abonelik DROP CONSTRAINT IF EXISTS abonelik_urun_check;
ALTER TABLE kredi.abonelik ADD CONSTRAINT abonelik_urun_check
    CHECK (urun IN ('plus.monthly', 'plus.yearly', 'pro.monthly', 'pro.yearly'));

ALTER TABLE kredi.abonelik
    ADD COLUMN IF NOT EXISTS sonraki_urun text NULL;

-- Kampanyalar: ayni anda en fazla bir aktif kampanya.
CREATE TABLE IF NOT EXISTS kredi.kampanya (
    id              bigserial PRIMARY KEY,
    ad              text NOT NULL,
    kota            integer NOT NULL CHECK (kota > 0),
    kisi_basi       integer NOT NULL CHECK (kisi_basi > 0),
    gecerlilik_gun  integer NOT NULL CHECK (gecerlilik_gun > 0),
    bitis_tarihi    date NULL,
    durum           text NOT NULL
        CHECK (durum IN ('aktif', 'kota_doldu', 'bitti', 'durduruldu')),
    katilan         integer NOT NULL DEFAULT 0,
    baslangic       timestamptz NOT NULL,
    durdurma        timestamptz NULL,
    olusturan       text NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS kampanya_tek_aktif_idx
    ON kredi.kampanya ((true)) WHERE durum = 'aktif';

-- Kisi omrunde bir hediye: kullanici ve e-posta karmasi tekil. Hesap
-- silinince kullanici_id ve dilim_id bosalir, eposta_karma kalir.
CREATE TABLE IF NOT EXISTS kredi.kampanya_katilim (
    kampanya_id   bigint NOT NULL,
    kullanici_id  uuid NULL UNIQUE,
    eposta_karma  text NOT NULL UNIQUE,
    dilim_id      bigint NULL,
    olusturuldu   timestamptz NOT NULL DEFAULT now()
);
