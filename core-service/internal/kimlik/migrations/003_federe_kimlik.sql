-- Federe kimlik eslesmeleri: (saglayici, saglayicidaki sub) -> bizim user_id.
--
-- Birincil anahtar (saglayici, sub) CIFTIDIR: ayni sub degeri iki ayri
-- saglayicida ayni kisi demek degildir, yani tek basina sub tekil degildir.
CREATE TABLE IF NOT EXISTS federe_kimlikler (
    saglayici  text NOT NULL,
    sub        text NOT NULL,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (saglayici, sub)
);

-- Hesap silme ve "bu hesap hangi saglayicilara bagli" sorgusu user_id'ye
-- gore arar; FK kendiliginden indeks kurmadigi icin acikca ekliyoruz.
CREATE INDEX IF NOT EXISTS federe_kimlikler_user_idx ON federe_kimlikler (user_id);

-- Bir kullanici ayni saglayiciya IKI KEZ baglanamaz: aksi halde
-- "Google'la giris yap" dedigimiz hesapta iki ayri Google sub'i olur ve
-- hangisinin gecerli oldugu belirsizlesir.
CREATE UNIQUE INDEX IF NOT EXISTS federe_kimlikler_user_saglayici_key
    ON federe_kimlikler (user_id, saglayici);
