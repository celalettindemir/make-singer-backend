package kimlik

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// federeKurulum, ayni sozlesmeyi iki uygulamaya da kosturur.
type federeKurulum struct {
	ad        string
	depo      FedereDepo
	kullanici UserStore
}

// federeDepoKurulumlari, sahte uygulamayi her zaman ekler; Postgres
// uygulamasini ise testPostgresUserStore ile BIREBIR AYNI kalipla kurar:
// KIMLIK_TEST_DB_URL ortam degiskeni, yoksa varsayilan yerel DSN, Baglan +
// Migrate, baglanamazsa t.Skip, t.Cleanup(havuz.Close). Postgres kaliciydir,
// bu yuzden her kosuda federe_kimlikler ve users temizlenir.
func federeDepoKurulumlari(t *testing.T) []federeKurulum {
	t.Helper()

	sahteKullanici := NewSahteUserStore()
	kurulumlar := []federeKurulum{
		{
			ad:        "sahte",
			depo:      NewSahteFedereDepo(sahteKullanici),
			kullanici: sahteKullanici,
		},
	}

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
	if _, err := havuz.Exec(ctx, `TRUNCATE federe_kimlikler, users CASCADE`); err != nil {
		t.Fatalf("tablolar temizlenemedi: %v", err)
	}

	kurulumlar = append(kurulumlar, federeKurulum{
		ad:        "postgres",
		depo:      NewPostgresFedereDepo(havuz),
		kullanici: NewPostgresUserStore(havuz),
	})

	return kurulumlar
}

func TestFedereDepoSozlesmesi(t *testing.T) {
	for _, k := range federeDepoKurulumlari(t) {
		t.Run(k.ad, func(t *testing.T) {
			ctx := context.Background()

			// Bilinmeyen kimlik ErrKimlikYok doner, nil kullanici degil.
			if _, err := k.depo.KimlikleKullanici(ctx, "google", "yok-123"); !errors.Is(err, ErrKimlikYok) {
				t.Fatalf("bilinmeyen kimlik icin err = %v, beklenen ErrKimlikYok", err)
			}

			// Federe kullanici olusturma: sifre hash'i BOS olmali, yani
			// SifreDogru her sifre icin false donmeli.
			ku, err := k.depo.FedereKullaniciOlustur(ctx, "google", "sub-1", "a@ornek.com", "Ali", true)
			if err != nil {
				t.Fatalf("federe kullanici olusturulamadi: %v", err)
			}
			if ku.PasswordHash != "" {
				t.Error("federe hesaba sifre hash'i yazilmis")
			}
			if ku.SifreDogru("herhangi-bir-sifre-123") {
				t.Error("sifresiz hesaba sifreyle girilebiliyor")
			}
			if !ku.EmailVerified {
				t.Error("epostaDogrulandi=true verildi ama EmailVerified false")
			}

			// Olusturulan kimlik artik bulunabilir ve AYNI kullaniciyi doner.
			bulunan, err := k.depo.KimlikleKullanici(ctx, "google", "sub-1")
			if err != nil {
				t.Fatalf("kimlik okunamadi: %v", err)
			}
			if bulunan.ID != ku.ID {
				t.Errorf("kullanici id = %q, beklenen %q", bulunan.ID, ku.ID)
			}

			// Saglayici ayrimi: ayni sub degeri baska saglayicida AYNI kisi
			// degildir.
			if _, err := k.depo.KimlikleKullanici(ctx, "apple", "sub-1"); !errors.Is(err, ErrKimlikYok) {
				t.Errorf("apple/sub-1 icin err = %v, beklenen ErrKimlikYok", err)
			}

			// Mevcut (sifreli) hesaba baglama.
			sifreli, err := k.kullanici.Create(ctx, "b@ornek.com", "Veli", "cok-uzun-bir-sifre")
			if err != nil {
				t.Fatalf("sifreli hesap olusturulamadi: %v", err)
			}
			if err := k.depo.Bagla(ctx, "apple", "apple-sub-9", sifreli.ID); err != nil {
				t.Fatalf("bagla: %v", err)
			}
			bagli, err := k.depo.KimlikleKullanici(ctx, "apple", "apple-sub-9")
			if err != nil {
				t.Fatalf("baglanan kimlik okunamadi: %v", err)
			}
			if bagli.ID != sifreli.ID {
				t.Error("bagli kimlik yanlis kullaniciyi donuyor")
			}

			// AYNI (saglayici, sub) BASKA bir hesaba baglanamaz: aksi halde
			// bir Google hesabi iki bizim hesabimiza birden baglanir ve
			// giriste hangisine dusecegi belirsizlesir.
			if err := k.depo.Bagla(ctx, "apple", "apple-sub-9", ku.ID); !errors.Is(err, ErrKimlikKullanimda) {
				t.Errorf("ikinci bagla err = %v, beklenen ErrKimlikKullanimda", err)
			}

			// Bir kullanici ayni saglayiciya iki kez baglanamaz.
			if err := k.depo.Bagla(ctx, "apple", "apple-sub-FARKLI", sifreli.ID); !errors.Is(err, ErrKimlikKullanimda) {
				t.Errorf("ayni saglayiciya ikinci bagla err = %v, beklenen ErrKimlikKullanimda", err)
			}

			// SaglayicilarByUserID, K1'in 409 sayfasinin metnini besler.
			sag, err := k.depo.SaglayicilarByUserID(ctx, sifreli.ID)
			if err != nil {
				t.Fatalf("saglayicilar okunamadi: %v", err)
			}
			if len(sag) != 1 || sag[0] != "apple" {
				t.Errorf("saglayicilar = %v, beklenen [apple]", sag)
			}
		})
	}
}
