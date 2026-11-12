CREATE TABLE IF NOT EXISTS uretim.ses_araliklari (
    sahip       text PRIMARY KEY,
    alt         int NOT NULL,
    ust         int NOT NULL,
    merkez      int NOT NULL,
    kaynak      text NOT NULL,
    olcum       timestamptz NOT NULL,
    guncelleme  timestamptz NOT NULL DEFAULT now()
);
