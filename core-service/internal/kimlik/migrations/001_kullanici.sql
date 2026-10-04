CREATE TABLE IF NOT EXISTS users (
    id             uuid PRIMARY KEY,
    email          text NOT NULL,
    email_verified boolean NOT NULL DEFAULT false,
    name           text NOT NULL DEFAULT '',
    password_hash  text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- E-posta tekilligi buyuk/kucuk harf duyarsiz olmali: "Ali@x.com" ile
-- "ali@x.com" ayni hesaptir. Kolona degil, lower(email) ifadesine index.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_key ON users (lower(email));

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         uuid PRIMARY KEY,
    family_id  uuid NOT NULL,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id  text NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    scopes     text[] NOT NULL DEFAULT '{}',
    audience   text[] NOT NULL DEFAULT '{}',
    amr        text[] NOT NULL DEFAULT '{}',
    auth_time  timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Calinti jeton tespiti: bir aile toptan iptal edilecegi icin aileye gore
-- arama sicak yoldur.
CREATE INDEX IF NOT EXISTS refresh_tokens_family_idx ON refresh_tokens (family_id);
