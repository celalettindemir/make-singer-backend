package kimlik

import (
	"strings"
	"testing"
)

// Migration dosyalari gomulu olmali: imaj icinde SQL dosyasi ayrica
// tasinmaz. Bu test dosyanin gercekten binary'ye girdigini dogrular.
func TestMigrationlarGomulu(t *testing.T) {
	girdiler, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("migrations dizini okunamadi: %v", err)
	}
	if len(girdiler) == 0 {
		t.Fatal("migrations dizini bos, go:embed calismamis")
	}
	icerik, err := migrationFS.ReadFile("migrations/001_kullanici.sql")
	if err != nil {
		t.Fatalf("001_kullanici.sql okunamadi: %v", err)
	}
	for _, beklenen := range []string{
		"CREATE TABLE IF NOT EXISTS users",
		"users_email_lower_key",
		"CREATE TABLE IF NOT EXISTS refresh_tokens",
		"refresh_tokens_family_idx",
	} {
		if !strings.Contains(string(icerik), beklenen) {
			t.Errorf("migration %q icermiyor", beklenen)
		}
	}
}

func TestMigrationSirali(t *testing.T) {
	adlar, err := migrationAdlari()
	if err != nil {
		t.Fatalf("migrationAdlari: %v", err)
	}
	for i := 1; i < len(adlar); i++ {
		if adlar[i-1] >= adlar[i] {
			t.Errorf("migrationlar sirali degil: %q, %q", adlar[i-1], adlar[i])
		}
	}
}
