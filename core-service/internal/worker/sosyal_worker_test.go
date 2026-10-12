package worker

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/sosyal"
)

const (
	sYayin = "9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	sSahip = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

type sahteHazirlikDeposu struct {
	kayit       *sosyal.HazirlikKaydi
	okumaHata   error
	yayinlanan  *sosyal.HazirlikSonucu
	hataYapildi bool
}

func (d *sahteHazirlikDeposu) HazirlikGetir(context.Context, string) (*sosyal.HazirlikKaydi, error) {
	if d.okumaHata != nil {
		return nil, d.okumaHata
	}
	if d.kayit == nil {
		return nil, sosyal.ErrBulunamadi
	}
	return d.kayit, nil
}

func (d *sahteHazirlikDeposu) Yayinla(_ context.Context, _ string, s sosyal.HazirlikSonucu) error {
	d.yayinlanan = &s
	return nil
}

func (d *sahteHazirlikDeposu) HataYap(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err // gercek havuz da olu baglamda sorgu calistirmaz
	}
	d.hataYapildi = true
	return nil
}

type kopya struct{ kaynak, hedef string }

type sahteKopyalayici struct {
	kopyalar []kopya
	hata     error
}

func (k *sahteKopyalayici) Copy(_ context.Context, kaynak, hedef string) error {
	if k.hata != nil {
		return k.hata
	}
	k.kopyalar = append(k.kopyalar, kopya{kaynak, hedef})
	return nil
}

type sahteAnalizci struct {
	istek *client.AnalysisRequest
	yanit *client.AnalysisResponse
	hata  error
}

func (a *sahteAnalizci) Analyze(_ context.Context, r *client.AnalysisRequest) (*client.AnalysisResponse, error) {
	a.istek = r
	return a.yanit, a.hata
}

func tepeler(n int, deger float64) []float64 {
	p := make([]float64, n)
	for i := range p {
		p[i] = deger
	}
	return p
}

func hazirKayit() *sosyal.HazirlikKaydi {
	return &sosyal.HazirlikKaydi{
		ID: sYayin, SahipID: sSahip, Durum: sosyal.DurumHazirlaniyor,
		Hazirlik: sosyal.Hazirlik{
			MasterKey: "masters/" + sSahip + "/p1/m1.wav",
			Stems: []sosyal.HazirlikStemi{
				{ID: "drums", Enstruman: "drums", Sure: 184, Key: "stems/" + sSahip + "/p1/drums.mp3"},
				{ID: "bass", Enstruman: "bass", Sure: 184, Key: "stems/" + sSahip + "/p1/bass.mp3"},
			},
		},
	}
}

type sosyalKurulum struct {
	depo   *sahteHazirlikDeposu
	kopya  *sahteKopyalayici
	analiz *sahteAnalizci
	w      *SosyalWorker
}

func sosyalTestWorker(kayit *sosyal.HazirlikKaydi, denetci deletionmark.Checker) *sosyalKurulum {
	k := &sosyalKurulum{
		depo:   &sahteHazirlikDeposu{kayit: kayit},
		kopya:  &sahteKopyalayici{},
		analiz: &sahteAnalizci{yanit: &client.AnalysisResponse{Peaks: tepeler(64, 0.5), Duration: 184}},
	}
	k.w = NewSosyalWorker(k.depo, k.kopya, k.analiz, nil, denetci)
	return k
}

func hazirlaGorevi() *asynq.Task {
	return asynq.NewTask(sosyal.TaskTypeHazirla, []byte(`{"yayinId":"`+sYayin+`"}`))
}

func TestSosyalWorker_KopyalarAnalizEderYayinlar(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	beklenen := []kopya{
		{"masters/" + sSahip + "/p1/m1.wav", "sosyal/" + sYayin + "/master.wav"},
		{"stems/" + sSahip + "/p1/drums.mp3", "sosyal/" + sYayin + "/stems/drums.mp3"},
		{"stems/" + sSahip + "/p1/bass.mp3", "sosyal/" + sYayin + "/stems/bass.mp3"},
	}
	if len(k.kopya.kopyalar) != len(beklenen) {
		t.Fatalf("kopyalar = %v", k.kopya.kopyalar)
	}
	for i, b := range beklenen {
		if k.kopya.kopyalar[i] != b {
			t.Errorf("kopya %d = %v, beklenen %v", i, k.kopya.kopyalar[i], b)
		}
	}
	if *k.analiz.istek != (client.AnalysisRequest{InputKey: "sosyal/" + sYayin + "/master.wav", PreviewKey: "sosyal/" + sYayin + "/onizleme.mp3", PeakCount: 64, PreviewSeconds: 30}) {
		t.Fatalf("analiz istegi = %+v", *k.analiz.istek)
	}
	s := k.depo.yayinlanan
	if s == nil || s.MasterAnahtari != "sosyal/"+sYayin+"/master.wav" || len(s.DalgaFormu) != 64 || s.OnizlemeAnahtari != "sosyal/"+sYayin+"/onizleme.mp3" {
		t.Fatalf("sonuc = %+v", s)
	}
	if len(s.StemListesi) != 2 || s.StemListesi[1] != (sosyal.StemKaydi{ID: "bass", Enstruman: "bass", Sure: 184, Anahtar: "sosyal/" + sYayin + "/stems/bass.mp3"}) {
		t.Fatalf("stem listesi = %+v", s.StemListesi)
	}
	if k.depo.hataYapildi {
		t.Fatal("basarili is hata yapmamali")
	}
}

func TestSosyalWorker_YokSilinmisYaDaHazirOlanAtlanir(t *testing.T) {
	silinmis := hazirKayit()
	silinmis.Silindi = true
	yayinda := hazirKayit()
	yayinda.Durum = sosyal.DurumYayinda
	for ad, kayit := range map[string]*sosyal.HazirlikKaydi{"yok": nil, "silinmis": silinmis, "yayinda": yayinda} {
		k := sosyalTestWorker(kayit, nil)
		if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
			t.Errorf("%s: hata donmemeli: %v", ad, err)
		}
		if len(k.kopya.kopyalar) != 0 || k.depo.yayinlanan != nil {
			t.Errorf("%s: hicbir sey yapilmamali", ad)
		}
	}
}

func TestSosyalWorker_SilinmisSahipKopyalanmazHataOlur(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), &sahteDenetci{yanit: hepSilinmis})
	err := k.w.Hazirla(context.Background(), hazirlaGorevi())
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu: %v", err)
	}
	if len(k.kopya.kopyalar) != 0 || !k.depo.hataYapildi || k.depo.yayinlanan != nil {
		t.Fatalf("kopya=%d hata=%v", len(k.kopya.kopyalar), k.depo.hataYapildi)
	}
}

// Hesap kopyalarin ortasinda silinirse kalan kopyalar yapilmaz.
func TestSosyalWorker_SahipIsinOrtasindaSilinirse(t *testing.T) {
	ikinciDenetimdeSilinmis := func(n int) (bool, error) { return n >= 1, nil }
	k := sosyalTestWorker(hazirKayit(), &sahteDenetci{yanit: ikinciDenetimdeSilinmis})
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu: %v", err)
	}
	if len(k.kopya.kopyalar) != 1 || !k.depo.hataYapildi {
		t.Fatalf("yalnizca master kopyalanmali: %v", k.kopya.kopyalar)
	}
}

func TestSosyalWorker_DenetimHatasiTekrarDenenir(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), &sahteDenetci{yanit: func(int) (bool, error) { return false, errors.New("redis kapali") }})
	err := k.w.Hazirla(context.Background(), hazirlaGorevi())
	if err == nil || errors.Is(err, asynq.SkipRetry) || k.depo.hataYapildi {
		t.Fatalf("tekrar denenebilir hata bekleniyordu: %v hata=%v", err, k.depo.hataYapildi)
	}
}

func TestSosyalWorker_KopyaHatasiSonDenemedeHataOlur(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	k.kopya.hata = errors.New("r2 kapali")
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("tekrar denenebilir hata bekleniyordu: %v", err)
	}
	if k.depo.hataYapildi || k.depo.yayinlanan != nil {
		t.Fatal("son deneme degilken hata durumuna alinmamali")
	}
	k.w.sonDeneme = func(context.Context) bool { return true }
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err == nil {
		t.Fatal("hata donmeli")
	}
	if !k.depo.hataYapildi {
		t.Fatal("son denemede yayin hata olmali")
	}
}

// Son denemede is suresi dolarsa (DeadlineExceeded) yayin yine hata olur.
func TestSosyalWorker_SonDenemedeSureDolarsaHataOlur(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	k.kopya.hata = context.DeadlineExceeded
	k.w.sonDeneme = func(context.Context) bool { return true }
	ctx, iptal := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer iptal()
	if err := k.w.Hazirla(ctx, hazirlaGorevi()); err == nil {
		t.Fatal("hata donmeli")
	}
	if !k.depo.hataYapildi {
		t.Fatal("suresi dolan son denemede yayin hata olmali")
	}
}

// Kapanista (Canceled) asynq isi yeniden kuyruga alir; yayin hata olmamali.
func TestSosyalWorker_IptalEdilenIsHataYapmaz(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	k.kopya.hata = context.Canceled
	k.w.sonDeneme = func(context.Context) bool { return true }
	ctx, iptal := context.WithCancel(context.Background())
	iptal()
	if err := k.w.Hazirla(ctx, hazirlaGorevi()); !errors.Is(err, context.Canceled) {
		t.Fatalf("iptal hatasi donmeli: %v", err)
	}
	if k.depo.hataYapildi {
		t.Fatal("iptal edilen is hata durumuna alinmamali")
	}
}

func TestSosyalWorker_AnalizHatasiYayiniDurdurmaz(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	k.analiz.hata = errors.New("audio-service kapali")
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("analiz hatasi isi bozmamali: %v", err)
	}
	s := k.depo.yayinlanan
	if s == nil || s.DalgaFormu == nil || len(s.DalgaFormu) != 0 || s.OnizlemeAnahtari != "" {
		t.Fatalf("bos dalga formu, onizlemesiz yayinlanmali: %+v", s)
	}
}

func TestSosyalWorker_AnalizCiktisiDenetlenir(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	k.analiz.yanit = &client.AnalysisResponse{Peaks: tepeler(10, 0.5)}
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	if len(k.depo.yayinlanan.DalgaFormu) != 0 {
		t.Fatalf("64 olmayan tepe dizisi atilmali: %v", k.depo.yayinlanan.DalgaFormu)
	}

	k = sosyalTestWorker(hazirKayit(), nil)
	p := tepeler(64, 0.5)
	p[0], p[1], p[2] = -0.2, 1.7, math.NaN()
	k.analiz.yanit = &client.AnalysisResponse{Peaks: p}
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	d := k.depo.yayinlanan.DalgaFormu
	if d[0] != 0 || d[1] != 1 || d[2] != 0 || d[3] != 0.5 {
		t.Fatalf("degerler 0-1'e kenetlenmeli: %v", d[:4])
	}
}

func TestSosyalWorker_R2VeAnalizYokken(t *testing.T) {
	d := &sahteHazirlikDeposu{kayit: hazirKayit()}
	w := NewSosyalWorker(d, nil, nil, nil, nil)
	if err := w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	if d.yayinlanan == nil || d.yayinlanan.MasterAnahtari != "sosyal/"+sYayin+"/master.wav" || len(d.yayinlanan.DalgaFormu) != 0 {
		t.Fatalf("sonuc = %+v", d.yayinlanan)
	}
}

func TestSosyalWorker_BozukYukTekrarlanmaz(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	for _, yuk := range []string{"{", `{"yayinId":"../x"}`} {
		if err := k.w.Hazirla(context.Background(), asynq.NewTask(sosyal.TaskTypeHazirla, []byte(yuk))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: SkipRetry bekleniyordu: %v", yuk, err)
		}
		if err := k.w.DosyaSil(context.Background(), asynq.NewTask(sosyal.TaskTypeDosyaSil, []byte(yuk))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: DosyaSil SkipRetry bekleniyordu: %v", yuk, err)
		}
	}
}

type sosyalSilici struct {
	onekler []string
	hata    error
}

func (s *sosyalSilici) DeletePrefix(_ context.Context, onek string) (int, error) {
	s.onekler = append(s.onekler, onek)
	return 1, s.hata
}

func TestSosyalWorker_DosyaSil(t *testing.T) {
	gorev := asynq.NewTask(sosyal.TaskTypeDosyaSil, []byte(`{"yayinId":"`+sYayin+`"}`))
	silinmis := hazirKayit()
	silinmis.Silindi = true

	durumlar := []struct {
		ad      string
		kayit   *sosyal.HazirlikKaydi
		silinir bool
	}{
		{"silinmis", silinmis, true},
		{"satir yok", nil, true},
		{"canli", hazirKayit(), false},
	}
	for _, d := range durumlar {
		s := &sosyalSilici{}
		w := NewSosyalWorker(&sahteHazirlikDeposu{kayit: d.kayit}, nil, nil, s, nil)
		if err := w.DosyaSil(context.Background(), gorev); err != nil {
			t.Fatalf("%s: %v", d.ad, err)
		}
		if d.silinir != (len(s.onekler) == 1 && s.onekler[0] == "sosyal/"+sYayin+"/") {
			t.Errorf("%s: onekler = %v", d.ad, s.onekler)
		}
	}

	s := &sosyalSilici{hata: errors.New("r2 kapali")}
	w := NewSosyalWorker(&sahteHazirlikDeposu{kayit: silinmis}, nil, nil, s, nil)
	if err := w.DosyaSil(context.Background(), gorev); err == nil {
		t.Fatal("R2 hatasi tekrar denensin diye donmeli")
	}
	w = NewSosyalWorker(&sahteHazirlikDeposu{okumaHata: errors.New("pg kapali")}, nil, nil, &sosyalSilici{}, nil)
	if err := w.DosyaSil(context.Background(), gorev); err == nil {
		t.Fatal("okuma hatasi donmeli")
	}
}
