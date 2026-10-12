package sosyal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
)

// Kuyruk gorevleri. Sabitler is turu adlaridir; main bunlari isleyicilere
// baglar.
const (
	// TaskTypeHazirla, yayinin dosyalarini kopyalar, analiz eder ve
	// yayinda yapar (sosyal kuyrugu).
	TaskTypeHazirla = "sosyal:hazirla"
	// TaskTypeDosyaSil, silinen yayinin sosyal/{id}/ onekini 24 saat
	// sonra temizler (bakim kuyrugu).
	TaskTypeDosyaSil = "sosyal:dosya-sil"

	// KuyrukSosyal, hazirlama islerinin asynq kuyrugu.
	KuyrukSosyal = "sosyal"

	hazirlaDenemesi     = 3
	hazirlaSaklama      = 24 * time.Hour
	dosyaSilmeGecikmesi = 24 * time.Hour
	dosyaSilDenemesi    = 10
	slugDenemesi        = 5
)

// GorevYuku, iki sosyal gorevinin yukudur.
type GorevYuku struct {
	YayinID string `json:"yayinId"`
}

func gorevYuku(yayinID string) []byte {
	b, _ := json.Marshal(GorevYuku{YayinID: yayinID})
	return b
}

func (s *Servis) hazirlamayaKoy(yayinID string) error {
	_, err := s.kuyruk.Enqueue(asynq.NewTask(TaskTypeHazirla, gorevYuku(yayinID)),
		asynq.Queue(KuyrukSosyal),
		asynq.MaxRetry(hazirlaDenemesi),
		asynq.Retention(hazirlaSaklama),
	)
	return err
}

// anahtarCoz, istemcinin gonderdigi adresi (tam URL ya da ciplak anahtar)
// sahiplik denetimiyle anahtara cevirir. Public bucket'taki (exports/)
// dosya reddedilir: kopya ayni bucket icinde yapilir.
func (s *Servis) anahtarCoz(ham, kullaniciID, alan string) (string, error) {
	key, err := client.ResolveOwnedKeyOrURL(ham, kullaniciID, s.r2)
	switch {
	case errors.Is(err, client.ErrKeyNotOwned):
		return "", ErrSahiplik
	case err != nil:
		return "", dogrulamaHatasi(alan + " gecersiz dosya adresi")
	case client.IsPublicKey(key):
		return "", dogrulamaHatasi(alan + " ozel bir dosya olmali")
	}
	return key, nil
}

// kaynakCoz, cover'in orijinalini dogrular: izleyiciye gorunur, yayinda,
// kendisi cover degil ve altyapisi acik olmali.
func (s *Servis) kaynakCoz(ctx context.Context, hamID, izleyiciID string) (*YayinKaydi, error) {
	id, ok := kimlikCoz(hamID)
	if !ok {
		return nil, ErrKaynakBulunamadi
	}
	k, err := s.depo.YayinGetir(ctx, id, izleyiciID)
	if errors.Is(err, ErrBulunamadi) {
		return nil, ErrKaynakBulunamadi
	}
	if err != nil {
		return nil, err
	}
	if !DetayGorebilir(k.gorunurlukGirdisi(izleyiciID)) || k.Durum != DurumYayinda || k.KaynakYayinID != "" {
		return nil, ErrKaynakBulunamadi
	}
	if !k.AltyapiAcik {
		return nil, ErrAltyapiKapali
	}
	return k, nil
}

// YayinOlustur, yayini hazirlaniyor durumunda kaydeder ve sosyal:hazirla
// isini kuyruga koyar (POST /yayinlar, 202).
func (s *Servis) YayinOlustur(ctx context.Context, kullaniciID string, ist YayinIstegi) (*YayinYaniti, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	if _, err := s.depo.ProfilGetir(ctx, id); err != nil {
		return nil, err
	}
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		return nil, err
	}

	yeni := YeniYayin{
		ID: uuid.NewString(), SahipID: id,
		Baslik: ist.Baslik, Aciklama: ist.Aciklama, Tur: ist.Tur, Bpm: ist.Bpm, Ton: ist.Ton,
		SureSn: ist.SureSn, Gorunurluk: ist.Gorunurluk, AltyapiAcik: ist.AltyapiAcik,
		YorumAcik: ist.YorumAcik, Sozler: ist.Sozler, Yapi: ist.Yapi,
		Hazirlik: Hazirlik{Stems: []HazirlikStemi{}},
	}
	if ist.KaynakYayinID != nil {
		kaynak, err := s.kaynakCoz(ctx, *ist.KaynakYayinID, id)
		if err != nil {
			return nil, err
		}
		yeni.KaynakYayinID = kaynak.ID
		yeni.KaynakBaslik = kaynak.Baslik
		yeni.KaynakSahipAdi = kaynak.Sahip.KullaniciAdi
	}

	if yeni.Hazirlik.MasterKey, err = s.anahtarCoz(ist.MasterURL, id, "masterUrl"); err != nil {
		return nil, err
	}
	for _, st := range ist.Stemler {
		key, err := s.anahtarCoz(st.URL, id, "stem url")
		if err != nil {
			return nil, err
		}
		yeni.Hazirlik.Stems = append(yeni.Hazirlik.Stems, HazirlikStemi{
			ID: st.ID, Enstruman: st.Enstruman, Sure: st.Sure, Key: key,
		})
	}

	for deneme := 1; ; deneme++ {
		if yeni.Slug, err = slugUret(); err != nil {
			return nil, fmt.Errorf("slug uretilemedi: %w", err)
		}
		err = s.depo.YayinEkle(ctx, yeni)
		if !errors.Is(err, errSlugCakisti) || deneme == slugDenemesi {
			break
		}
	}
	if err != nil {
		return nil, err
	}

	if err := s.hazirlamayaKoy(yeni.ID); err != nil {
		// Kuyruga girmeyen yayin hic hazirlanmaz; sahipsiz bir
		// "hazirlaniyor" satiri birakmak yerine geri alinir. Istek baglami
		// (or. kapanista) iptal edilmis olsa da geri alma calismalidir.
		if kerr := s.depo.YayinKaldir(context.WithoutCancel(ctx), yeni.ID); kerr != nil {
			log.Printf("Uyari: kuyruga girmeyen yayin geri alinamadi (%s): %v", yeni.ID, kerr)
		}
		return nil, fmt.Errorf("hazirlama isi kurulamadi: %w", err)
	}
	return &YayinYaniti{ID: yeni.ID, Slug: yeni.Slug, Durum: DurumHazirlaniyor}, nil
}

// gorunurYayin, yayini yukler ve detay gorunurlugunu uygular.
func (s *Servis) gorunurYayin(ctx context.Context, izleyiciID, hamYayinID string) (*YayinKaydi, error) {
	yayinID, ok := kimlikCoz(hamYayinID)
	if !ok {
		return nil, ErrBulunamadi
	}
	k, err := s.depo.YayinGetir(ctx, yayinID, izleyiciID)
	if err != nil {
		return nil, err
	}
	if !DetayGorebilir(k.gorunurlukGirdisi(izleyiciID)) {
		return nil, ErrBulunamadi
	}
	return k, nil
}

// sahibinYayini, yayini yukler ve istegi yapanin sahibi oldugunu, yayinin
// silinmedigini dogrular. Aksi ErrBulunamadi'dir: baskasinin yayininin
// varligi da sizdirilmaz.
func (s *Servis) sahibinYayini(ctx context.Context, sahipID, hamYayinID string) (*YayinKaydi, error) {
	yayinID, ok := kimlikCoz(hamYayinID)
	if !ok {
		return nil, ErrBulunamadi
	}
	k, err := s.depo.YayinGetir(ctx, yayinID, sahipID)
	if err != nil {
		return nil, err
	}
	if k.SahipID != sahipID || k.Silindi {
		return nil, ErrBulunamadi
	}
	return k, nil
}

// YayinGoster, GET /yayinlar/{id}.
func (s *Servis) YayinGoster(ctx context.Context, kullaniciID, yayinID string) (*YayinDetayi, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	return &k.YayinDetayi, nil
}

// SlugGoster, GET /s/{slug}.
func (s *Servis) SlugGoster(ctx context.Context, kullaniciID, slug string) (*YayinDetayi, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	if !SlugGecerli(slug) {
		return nil, ErrBulunamadi
	}
	k, err := s.depo.YayinSlugIleGetir(ctx, slug, id)
	if err != nil {
		return nil, err
	}
	if !DetayGorebilir(k.gorunurlukGirdisi(id)) {
		return nil, ErrBulunamadi
	}
	return &k.YayinDetayi, nil
}

// YayinGuncelle, PATCH /yayinlar/{id}. Altyapi yalnizca stem kopyasi olan
// (yayinlanirken altyapisi acik) orijinallerde yeniden acilabilir.
func (s *Servis) YayinGuncelle(ctx context.Context, kullaniciID, yayinID string, yama YayinYamasi) (*YayinDetayi, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	if err := yamayiDuzenle(&yama); err != nil {
		return nil, err
	}
	k, err := s.sahibinYayini(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if yama.AltyapiAcik != nil && *yama.AltyapiAcik && !k.AltyapiAcik && (!k.StemVar || k.KaynakYayinID != "") {
		return nil, ErrStemYok
	}
	if err := s.depo.YayinGuncelle(ctx, k.ID, id, yama); err != nil {
		return nil, err
	}
	return s.YayinGoster(ctx, id, k.ID)
}

// YayinSil, DELETE /yayinlar/{id}: yumusak siler ve dosya temizligini 24
// saat sonraya kurar. Kuyruk hatasi loglanir; hesap silme ayni oneki
// yeniden temizler.
func (s *Servis) YayinSil(ctx context.Context, kullaniciID, yayinID string) error {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return err
	}
	yid, ok := kimlikCoz(yayinID)
	if !ok {
		return ErrBulunamadi
	}
	if err := s.depo.YayinSil(ctx, yid, id); err != nil {
		return err
	}
	if _, err := s.kuyruk.Enqueue(asynq.NewTask(TaskTypeDosyaSil, gorevYuku(yid)),
		asynq.Queue("bakim"),
		asynq.ProcessIn(dosyaSilmeGecikmesi),
		asynq.MaxRetry(dosyaSilDenemesi),
	); err != nil {
		log.Printf("Uyari: yayin dosya temizligi kurulamadi (%s): %v", yid, err)
	}
	return nil
}

// TekrarDene, POST /yayinlar/{id}/tekrar-dene: hata durumundaki yayini
// yeniden hazirlamaya alir.
func (s *Servis) TekrarDene(ctx context.Context, kullaniciID, yayinID string) (*YayinYaniti, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.sahibinYayini(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if err := s.depo.TekrarDenemeyeAl(ctx, k.ID, id); err != nil {
		return nil, err
	}
	if err := s.hazirlamayaKoy(k.ID); err != nil {
		// Iptal edilmis istek baglaminda da yayin "hazirlaniyor"da kalmamali.
		if herr := s.depo.HataYap(context.WithoutCancel(ctx), k.ID); herr != nil {
			log.Printf("Uyari: kuyruga girmeyen yayin hataya alinamadi (%s): %v", k.ID, herr)
		}
		return nil, fmt.Errorf("hazirlama isi kurulamadi: %w", err)
	}
	return &YayinYaniti{ID: k.ID, Slug: k.Slug, Durum: DurumHazirlaniyor}, nil
}

// Dosyalar, POST /yayinlar/{id}/dosyalar: master kopyasi icin imzali
// dinleme adresi (R2_PRESIGN_TTL, varsayilan 1 saat).
func (s *Servis) Dosyalar(ctx context.Context, kullaniciID, yayinID string) (*YayinDosyalari, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if k.Durum != DurumYayinda || k.MasterAnahtari == "" {
		return nil, ErrDurum
	}
	url, bitis, err := client.ResolveURL(ctx, s.depolama, s.r2, k.MasterAnahtari)
	if err != nil {
		return nil, fmt.Errorf("dinleme adresi uretilemedi: %w", err)
	}
	adres := DosyaAdresi{URL: url}
	if !bitis.IsZero() {
		adres.ExpiresAt = &bitis
	}
	return &YayinDosyalari{Master: adres}, nil
}
