package identity

import (
	"strings"
	"testing"
)

// The migration files must be embedded: the image does not ship the SQL
// files separately. This test checks that the file really got into the binary.
func TestMigrations_Embedded(t *testing.T) {
	inputs, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("migrations dizini okunamadi: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("migrations dizini bos, go:embed calismamis")
	}
	content, err := migrationFS.ReadFile("migrations/001_kullanici.sql")
	if err != nil {
		t.Fatalf("001_kullanici.sql okunamadi: %v", err)
	}
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS users",
		"users_email_lower_key",
		"CREATE TABLE IF NOT EXISTS refresh_tokens",
		"refresh_tokens_family_idx",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("migration %q icermiyor", want)
		}
	}
	// 002 must be embedded too: the (user_id, client_id) lookup of RevokeUser
	// relies on this index.
	second, err := migrationFS.ReadFile("migrations/002_refresh_indeks.sql")
	if err != nil {
		t.Fatalf("002_refresh_indeks.sql okunamadi: %v", err)
	}
	for _, want := range []string{
		"CREATE INDEX IF NOT EXISTS refresh_tokens_user_client_idx",
		"(user_id, client_id)",
	} {
		if !strings.Contains(string(second), want) {
			t.Errorf("002 migration %q icermiyor", want)
		}
	}
}

func TestMigrations_Ordered(t *testing.T) {
	names, err := migrationNames()
	if err != nil {
		t.Fatalf("migrationAdlari: %v", err)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("migrationlar sirali degil: %q, %q", names[i-1], names[i])
		}
	}
}
