package kimlik

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// testPostgresUserStore, yerel Postgres ister. Yoksa test atlanir:
// birim testler agsiz gecmek zorunda (bkz. testRedis / testPostgres,
// AYNI kalip). DSN ortam degiskeninden alinir; varsayilan deger tek
// kullanimlik, TEST-YEREL bir konteyner icindir (uretim sirri DEGILDIR).
func testPostgresUserStore(t *testing.T) *PostgresUserStore {
	t.Helper()
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx, iptal := context.WithTimeout(context.Background(), 3*time.Second)
	defer iptal()
	havuz, err := Baglan(ctx, dsn)
	if err != nil {
		t.Skip("yerel Postgres yok, atlaniyor")
	}
	if err := Migrate(ctx, havuz); err != nil {
		havuz.Close()
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(havuz.Close)
	return NewPostgresUserStore(havuz)
}

// sozlesmeSonuc, bir UserStore cagrisinin GOZLENEBILIR sonucudur:
// sahte ile Postgres bu uc degerde BIREBIR ayni olmak zorunda. ID
// karsilastirilmaz (uuid, her depoda farkli).
type sozlesmeSonuc struct {
	bulundu bool
	eposta  string
	ad      string
	hata    string // hatanin TURU ("", "kullanici-yok", "eposta-kullanimda", "sifre-kisa", "diger")
}

func sozlesmeHata(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrKullaniciYok):
		return "kullanici-yok"
	case errors.Is(err, ErrEpostaKullanimda):
		return "eposta-kullanimda"
	case errors.Is(err, ErrSifreKisa):
		return "sifre-kisa"
	default:
		return "diger: " + err.Error()
	}
}

func sonuc(k *User, err error) sozlesmeSonuc {
	s := sozlesmeSonuc{hata: sozlesmeHata(err)}
	if k != nil {
		s.bulundu = true
		s.eposta = k.Email
		s.ad = k.Name
	}
	return s
}

// SOZLESME TESTI: SahteUserStore ile PostgresUserStore AYNI girdilerde
// AYNI seyi yapmali. Bu dalda ikisi ayrismisti ve ayrisma gercek bir
// uretim hatasini (bosluklu e-postayla kayit olan kullanici giris
// yapamiyor) gizliyordu. Postgres yoksa test ATLANIR; sahte depo
// tarafi her halde kosar ve beklenen degerlerle karsilastirilir.
func TestUserStoreSozlesmesi(t *testing.T) {
	const sifre = "gecerliSifre12"
	// Her kosuda taze e-posta: Postgres kalicidir, ayni e-posta ikinci
	// kosuda "kullanimda" cikar ve test kendi kendini kirar.
	temelEposta := "sozlesme-" + uuid.NewString() + "@ornek.com"
	bosluklu := "  " + temelEposta + "  "
	buyuk := strings.ToUpper(temelEposta)
	yokID := uuid.NewString()

	adimlar := []struct {
		ad       string
		cagri    func(context.Context, UserStore) sozlesmeSonuc
		beklenen sozlesmeSonuc
	}{
		{
			// Bastaki/sondaki bosluklar kirpilarak saklanir.
			ad: "Create bosluklu e-posta ve ad",
			cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
				return sonuc(d.Create(ctx, bosluklu, "  Ad Soyad  ", sifre))
			},
			beklenen: sozlesmeSonuc{bulundu: true, eposta: temelEposta, ad: "Ad Soyad"},
		},
		{
			// GERCEK URETIM HATASI: kullanici kayit oldugu GIRDIYLE
			// (bosluklu) giris yapabilmeli.
			ad: "ByEmail bosluklu girdi ile bulur",
			cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
				return sonuc(d.ByEmail(ctx, bosluklu))
			},
			beklenen: sozlesmeSonuc{bulundu: true, eposta: temelEposta, ad: "Ad Soyad"},
		},
		{
			ad: "ByEmail bosluksuz girdi ile bulur",
			cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
				return sonuc(d.ByEmail(ctx, temelEposta))
			},
			beklenen: sozlesmeSonuc{bulundu: true, eposta: temelEposta, ad: "Ad Soyad"},
		},
		{
			// "E-posta buyuk-kucuk harf duyarsiz tekil" baglayici bir
			// kisit: Postgres'te UNIQUE (lower(email)).
			ad: "ByEmail buyuk harfli girdi ile bulur",
			cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
				return sonuc(d.ByEmail(ctx, buyuk))
			},
			beklenen: sozlesmeSonuc{bulundu: true, eposta: temelEposta, ad: "Ad Soyad"},
		},
		{
			// Sahte depo eskiden IKINCI hesabi OLUSTURUYORDU.
			ad: "Create ayni e-postayi bosluk/harf farkiyla reddeder",
			cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
				return sonuc(d.Create(ctx, " "+buyuk+" ", "Baska", sifre))
			},
			beklenen: sozlesmeSonuc{hata: "eposta-kullanimda"},
		},
		{
			ad: "ByEmail olmayan e-posta",
			cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
				return sonuc(d.ByEmail(ctx, "yok-"+uuid.NewString()+"@ornek.com"))
			},
			beklenen: sozlesmeSonuc{hata: "kullanici-yok"},
		},
		{
			ad: "ByID olmayan kimlik",
			cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
				return sonuc(d.ByID(ctx, yokID))
			},
			beklenen: sozlesmeSonuc{hata: "kullanici-yok"},
		},
		{
			ad: "Create kisa sifre",
			cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
				return sonuc(d.Create(ctx, "kisa-"+uuid.NewString()+"@ornek.com", "Ad", "kisa"))
			},
			beklenen: sozlesmeSonuc{hata: "sifre-kisa"},
		},
	}

	kos := func(t *testing.T, depo UserStore) {
		ctx := context.Background()
		for _, a := range adimlar {
			got := a.cagri(ctx, depo)
			if got != a.beklenen {
				t.Errorf("%s: sonuc = %+v, beklenen %+v", a.ad, got, a.beklenen)
			}
		}
	}

	t.Run("sahte", func(t *testing.T) { kos(t, NewSahteUserStore()) })
	t.Run("postgres", func(t *testing.T) { kos(t, testPostgresUserStore(t)) })

	// Create'in dondurdugu ID ile ByID gercekten bulunabilmeli. Bunu
	// adimlardan ayri olcuyoruz: ID depolar arasinda karsilastirilamaz.
	idKos := func(t *testing.T, depo UserStore) {
		ctx := context.Background()
		k, err := depo.Create(ctx, "id-"+uuid.NewString()+"@ornek.com", "Ad", sifre)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		bulunan, err := depo.ByID(ctx, k.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if bulunan.ID != k.ID || bulunan.Email != k.Email {
			t.Errorf("ByID = %+v, beklenen %+v", bulunan, k)
		}
	}
	t.Run("sahte-byid", func(t *testing.T) { idKos(t, NewSahteUserStore()) })
	t.Run("postgres-byid", func(t *testing.T) { idKos(t, testPostgresUserStore(t)) })
}

// SahteUserStore cagirana DAHILI isaretci VERMEMELI. Bu, commit
// 50e323e'nin SahteTokenStore icin duzelttigi kusurun AYNISI:
// PostgresUserStore her okumada satiri yeniden tarayarak dogal olarak
// taze bir yapi doner, sahte deponun ayni yalitimi ELLE saglamasi
// gerekir. Aksi halde cagiran donen *User'i mutasyona ugratinca depo
// bozulur ve testler gecerken uretim sasar.
func TestSahteUserStoreDahiliIsaretciVermez(t *testing.T) {
	depo := NewSahteUserStore()
	ctx := context.Background()
	k, err := depo.Create(ctx, "kopya@ornek.com", "Ad Soyad", "gecerliSifre12")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 1. Create'in dondurdugu kaydi bozmak depoyu bozmamali.
	k.Email = "bozuk@ornek.com"
	k.PasswordHash = ""

	ilk, err := depo.ByEmail(ctx, "kopya@ornek.com")
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if ilk.Email != "kopya@ornek.com" || ilk.PasswordHash == "" {
		t.Fatalf("Create'in dondurdugu kaydi mutasyona ugratmak depoyu bozdu: %+v", ilk)
	}

	// 2. ByEmail'in dondurdugu kaydi bozmak da depoyu bozmamali.
	ilk.PasswordHash = ""
	ilk.Name = "degisti"
	ikinci, err := depo.ByEmail(ctx, "kopya@ornek.com")
	if err != nil {
		t.Fatalf("ByEmail (ikinci): %v", err)
	}
	if ikinci.PasswordHash == "" || ikinci.Name != "Ad Soyad" {
		t.Errorf("ByEmail dahili isaretci dondurdu: %+v", ikinci)
	}

	// 3. ByID icin de ayni yalitim.
	idIle, err := depo.ByID(ctx, ikinci.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	idIle.Name = "yine degisti"
	tekrar, err := depo.ByID(ctx, ikinci.ID)
	if err != nil {
		t.Fatalf("ByID (ikinci): %v", err)
	}
	if tekrar.Name != "Ad Soyad" {
		t.Errorf("ByID dahili isaretci dondurdu: Name = %q", tekrar.Name)
	}
}

// epostaNormalize TEK yardimci: Create ve ByEmail ayni yoldan gecmek
// zorunda. Normalizasyon buyuk/kucuk harfi DEGISTIRMEZ (tekillik
// Postgres'te lower(email) uzerinden).
func TestEpostaNormalize(t *testing.T) {
	durumlar := map[string]string{
		"  x@y.com  ":      "x@y.com",
		"x@y.com":          "x@y.com",
		"\tx@y.com\n":      "x@y.com",
		"X@Y.com":          "X@Y.com",
		"   ":              "",
		"a b@y.com":        "a b@y.com",
		" Ad.Soyad@y.com ": "Ad.Soyad@y.com",
	}
	for girdi, beklenen := range durumlar {
		if got := epostaNormalize(girdi); got != beklenen {
			t.Errorf("epostaNormalize(%q) = %q, beklenen %q", girdi, got, beklenen)
		}
	}
}
