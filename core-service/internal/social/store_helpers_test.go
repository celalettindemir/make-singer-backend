package social

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPublication is a publication row to insert with raw SQL. Empty fields
// are filled with sensible defaults; so store tests can be built without
// waiting for the publication-insert path.
type testPublication struct {
	ID             string
	Slug           string
	OwnerID        string
	Title          string
	Visibility     string
	Status         string
	BackingEnabled bool
	SourceID       string
	CreatedAt      time.Time
	Deleted        bool
	Preparation    string
}

var slugCounter int

func testInsertPublication(t *testing.T, pool *pgxpool.Pool, row testPublication) string {
	t.Helper()
	if row.ID == "" {
		row.ID = uuid.NewString()
	}
	if row.Slug == "" {
		slugCounter++
		row.Slug = fmt.Sprintf("T%07d", slugCounter)
	}
	if row.Title == "" {
		row.Title = "Baslik"
	}
	if row.Visibility == "" {
		row.Visibility = VisibilityEveryone
	}
	if row.Status == "" {
		row.Status = StatusPublished
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if row.Preparation == "" {
		row.Preparation = `{"masterKey":"masters/u/p/m.wav","stems":[]}`
	}
	var source any
	if row.SourceID != "" {
		source = row.SourceID
	}
	var deletedAt any
	if row.Deleted {
		deletedAt = time.Now()
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, tur, bpm, ton_tonik, ton_mod, sure_sn,
			gorunurluk, altyapi_acik, yorum_acik, sozler, yapi, hazirlik, kaynak_yayin_id,
			kaynak_baslik, kaynak_sahip_adi, durum, olusturma, silinme)
		VALUES ($1, $2, $3, $4, 'pop', 120, 'A', 'minor', 180, $5, $6, true,
			'{"bolumler":[]}', '[]', $7, $8, CASE WHEN $8::uuid IS NULL THEN NULL ELSE 'Eski baslik' END,
			CASE WHEN $8::uuid IS NULL THEN NULL ELSE 'eski_ad' END, $9, $10, $11)`,
		row.ID, row.Slug, row.OwnerID, row.Title, row.Visibility, row.BackingEnabled, row.Preparation, source, row.Status, row.CreatedAt, deletedAt)
	if err != nil {
		t.Fatalf("test yayini eklenemedi: %v", err)
	}
	return row.ID
}

func testInsertFollow(t *testing.T, pool *pgxpool.Pool, follower, followee string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO sosyal.takip (takipci_id, takip_edilen_id) VALUES ($1, $2)`, follower, followee); err != nil {
		t.Fatalf("takip eklenemedi: %v", err)
	}
}

func testInsertProfile(t *testing.T, store *PostgresStore, id, name string) {
	t.Helper()
	if _, err := store.UpsertProfile(context.Background(), ProfileRecord{UserID: id, Username: name, DisplayName: name}); err != nil {
		t.Fatalf("profil eklenemedi: %v", err)
	}
}
