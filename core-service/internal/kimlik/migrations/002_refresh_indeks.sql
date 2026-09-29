-- KullaniciIptal (cikis) bu iki sutuna gore siliyor; FK otomatik indeks
-- kurmadigi icin acikca ekliyoruz.
CREATE INDEX IF NOT EXISTS refresh_tokens_user_client_idx
    ON refresh_tokens (user_id, client_id);
