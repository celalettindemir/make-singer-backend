package social

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresDepo_SikayetTekrariYeniKayitAcmaz(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip, bildiren := uuid.NewString(), uuid.NewString()
	yayin := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: "hazirlaniyor"})
	yorum := testYorumEkle(t, havuz, "", yayin, sahip, nil, time.Now())

	yeni := func(tur, hedef, neden string) ReportInsert {
		return ReportInsert{ID: uuid.NewString(), ReporterID: bildiren, TargetType: tur, TargetID: hedef, PublicationID: yayin, Reason: neden}
	}
	for _, s := range []ReportInsert{
		yeni(TargetPublication, yayin, "spam"),
		yeni(TargetPublication, yayin, "telif"), // tekrar: ilk kayit kalir
		yeni(TargetComment, yorum, "taciz"),
	} {
		if err := d.InsertReport(ctx, s); err != nil {
			t.Fatalf("SikayetEkle: %v", err)
		}
	}
	var n int
	var neden string
	if err := havuz.QueryRow(ctx, `SELECT count(*), min(neden) FILTER (WHERE hedef_turu = 'yayin')
		FROM sosyal.sikayet WHERE bildiren_id = $1 AND yayin_id = $2`, bildiren, yayin).Scan(&n, &neden); err != nil {
		t.Fatal(err)
	}
	if n != 2 || neden != "spam" {
		t.Fatalf("kayit sayisi %d, yayin nedeni %q; beklenen 2 ve ilk neden spam", n, neden)
	}

	// Yayin kalici silinince sikayetleri cascade ile gider.
	if err := d.RemovePublication(ctx, yayin); err != nil {
		t.Fatal(err)
	}
	if err := havuz.QueryRow(ctx, `SELECT count(*) FROM sosyal.sikayet WHERE bildiren_id = $1`, bildiren).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade sonrasi %d sikayet, %v", n, err)
	}
}
