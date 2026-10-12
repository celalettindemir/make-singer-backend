package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/sosyal"
)

const (
	// dalgaTepeSayisi ve onizlemeSaniyesi, sozlesmedeki 64 tepe ve 30 sn.
	dalgaTepeSayisi  = 64
	onizlemeSaniyesi = 30
)

// SosyalWorker, sosyal:hazirla ve sosyal:dosya-sil islerini yapar.
type SosyalWorker struct {
	depo        sosyal.HazirlikDeposu
	kopyalayici client.Kopyalayici   // nil: R2 yok, kopya atlanir
	analizci    client.AudioAnalizci // nil: dalga formu ve onizleme yok
	silici      client.OnekSilici    // nil: R2 yok
	hesap       deletionmark.Checker // nil: denetim yok
	sonDeneme   func(context.Context) bool
}

// NewSosyalWorker kurar. Arayuz parametreleri nil olabilir; main tipli nil
// tuzagina dusmemek icin yalnizca gercek istemcileri gecirir.
func NewSosyalWorker(depo sosyal.HazirlikDeposu, kopyalayici client.Kopyalayici, analizci client.AudioAnalizci,
	silici client.OnekSilici, hesap deletionmark.Checker) *SosyalWorker {
	return &SosyalWorker{
		depo: depo, kopyalayici: kopyalayici, analizci: analizci, silici: silici, hesap: hesap,
		sonDeneme: asynqSonDeneme,
	}
}

// asynqSonDeneme, bu denemenin isin son denemesi olup olmadigini soyler.
// Baglamda asynq bilgisi yoksa (test, elle cagri) false doner.
func asynqSonDeneme(ctx context.Context) bool {
	n, ok1 := asynq.GetRetryCount(ctx)
	enCok, ok2 := asynq.GetMaxRetry(ctx)
	return ok1 && ok2 && n >= enCok
}

func gorevYukunuCoz(t *asynq.Task) (string, error) {
	var yuk sosyal.GorevYuku
	if err := json.Unmarshal(t.Payload(), &yuk); err != nil {
		return "", fmt.Errorf("sosyal gorev yuku cozulemedi: %v: %w", err, asynq.SkipRetry)
	}
	id, err := uuid.Parse(yuk.YayinID)
	if err != nil {
		return "", fmt.Errorf("sosyal gorev yukunde gecersiz yayin kimligi: %w", asynq.SkipRetry)
	}
	return id.String(), nil
}

// Hazirla, yayinin dosyalarini sosyal/{id}/ altina kopyalar, analiz eder
// ve yayinda yapar. Kopya hatalari tekrar denenir; son denemede yayin
// hata olur. Analiz hatasi yayini durdurmaz: dalga formu bos kalir.
func (w *SosyalWorker) Hazirla(ctx context.Context, t *asynq.Task) error {
	id, err := gorevYukunuCoz(t)
	if err != nil {
		return err
	}
	k, err := w.depo.HazirlikGetir(ctx, id)
	if errors.Is(err, sosyal.ErrBulunamadi) {
		return nil
	}
	if err != nil {
		return err
	}
	if k.Silindi || k.Durum != sosyal.DurumHazirlaniyor {
		return nil
	}
	sonuc, err := w.kopyala(ctx, k)
	if err != nil {
		return w.basarisiz(ctx, id, err)
	}
	if err := w.sahipDenetle(ctx, k.SahipID); err != nil {
		return w.basarisiz(ctx, id, err)
	}
	w.analizEt(ctx, id, &sonuc)
	if err := w.depo.Yayinla(ctx, id, sonuc); err != nil {
		return w.basarisiz(ctx, id, err)
	}
	return nil
}

// sahipDenetle, her R2 yazimindan once is sahibinin silinmedigini dogrular.
// Silinmisse kalici hatadir (SkipRetry); denetim hatasi tekrar denenir.
func (w *SosyalWorker) sahipDenetle(ctx context.Context, sahipID string) error {
	if err := sahipYazabilir(ctx, w.hesap, sahipID); err != nil {
		if errors.Is(err, errSahipSilindi) {
			return fmt.Errorf("yayin hazirlanmadi: %w: %w", err, asynq.SkipRetry)
		}
		return err
	}
	return nil
}

// basarisiz, hatayi doner; kalici hatada ya da son denemede yayini hata
// durumuna alir ki sahibi "Tekrar dene" gorebilsin. Baglam iptal
// edildiyse (kapanis) asynq isi yeniden kuyruga alir; yayin isaretlenmez.
// Suresi dolmus baglamda da isaretlenebilsin diye HataYap iptalden
// bagimsiz bir baglamla cagrilir.
func (w *SosyalWorker) basarisiz(ctx context.Context, id string, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return err
	}
	if errors.Is(err, asynq.SkipRetry) || w.sonDeneme(ctx) {
		if herr := w.depo.HataYap(context.WithoutCancel(ctx), id); herr != nil {
			log.Printf("Uyari: yayin hata durumuna alinamadi (%s): %v", id, herr)
		}
	}
	return err
}

// kopyala, master ve stem kopyalarini kurar ve sonucun anahtar alanlarini
// doldurur. Ayni hedefe tekrar kopya zararsizdir; is tekrar denenebilir.
func (w *SosyalWorker) kopyala(ctx context.Context, k *sosyal.HazirlikKaydi) (sosyal.HazirlikSonucu, error) {
	var sonuc sosyal.HazirlikSonucu
	master, err := sosyal.MasterHedefi(k.ID, k.Hazirlik.MasterKey)
	if err != nil {
		return sonuc, fmt.Errorf("master hedefi kurulamadi: %v: %w", err, asynq.SkipRetry)
	}
	if err := w.kopyalaBir(ctx, k.SahipID, k.Hazirlik.MasterKey, master); err != nil {
		return sonuc, fmt.Errorf("master kopyalanamadi: %w", err)
	}
	sonuc.MasterAnahtari = master
	sonuc.StemListesi = make([]sosyal.StemKaydi, 0, len(k.Hazirlik.Stems))
	for _, st := range k.Hazirlik.Stems {
		hedef, err := sosyal.StemHedefi(k.ID, st.ID, st.Key)
		if err != nil {
			return sonuc, fmt.Errorf("stem hedefi kurulamadi: %v: %w", err, asynq.SkipRetry)
		}
		if err := w.kopyalaBir(ctx, k.SahipID, st.Key, hedef); err != nil {
			return sonuc, fmt.Errorf("stem kopyalanamadi: %w", err)
		}
		sonuc.StemListesi = append(sonuc.StemListesi, sosyal.StemKaydi{
			ID: st.ID, Enstruman: st.Enstruman, Sure: st.Sure, Anahtar: hedef,
		})
	}
	return sonuc, nil
}

func (w *SosyalWorker) kopyalaBir(ctx context.Context, sahipID, kaynak, hedef string) error {
	if err := w.sahipDenetle(ctx, sahipID); err != nil {
		return err
	}
	if w.kopyalayici == nil {
		return nil
	}
	return w.kopyalayici.Kopyala(ctx, kaynak, hedef)
}

// analizEt, dalga formu ve onizlemeyi uretir. Her hata loglanir ve
// sonuc bos dalga formuyla, onizlemesiz devam eder (spec §4.3).
func (w *SosyalWorker) analizEt(ctx context.Context, id string, sonuc *sosyal.HazirlikSonucu) {
	sonuc.DalgaFormu = []float64{}
	if w.analizci == nil {
		return
	}
	onizleme, err := sosyal.OnizlemeHedefi(id)
	if err != nil {
		log.Printf("Uyari: onizleme hedefi kurulamadi (%s): %v", id, err)
		return
	}
	yanit, err := w.analizci.Analiz(ctx, &client.AnalizRequest{
		InputKey: sonuc.MasterAnahtari, PreviewKey: onizleme,
		PeakCount: dalgaTepeSayisi, PreviewSeconds: onizlemeSaniyesi,
	})
	if err != nil {
		log.Printf("Uyari: yayin analiz edilemedi, dalga formu bos kalacak (%s): %v", id, err)
		return
	}
	if len(yanit.Peaks) != dalgaTepeSayisi {
		log.Printf("Uyari: analiz %d tepe dondu, %d bekleniyordu (%s)", len(yanit.Peaks), dalgaTepeSayisi, id)
		return
	}
	dalga := make([]float64, dalgaTepeSayisi)
	for i, p := range yanit.Peaks {
		if math.IsNaN(p) {
			p = 0
		}
		dalga[i] = math.Min(1, math.Max(0, p))
	}
	sonuc.DalgaFormu = dalga
	sonuc.OnizlemeAnahtari = onizleme
}

// DosyaSil, silinen yayinin sosyal/{id}/ onekini temizler. Satir hala
// canliysa (silinmemis) hicbir sey silinmez.
func (w *SosyalWorker) DosyaSil(ctx context.Context, t *asynq.Task) error {
	id, err := gorevYukunuCoz(t)
	if err != nil {
		return err
	}
	onek, err := client.SosyalOneki(id)
	if err != nil {
		return fmt.Errorf("sosyal onek kurulamadi: %v: %w", err, asynq.SkipRetry)
	}
	k, err := w.depo.HazirlikGetir(ctx, id)
	switch {
	case errors.Is(err, sosyal.ErrBulunamadi):
		// Satir yok (hesap silindi): dosyalar yine de temizlenir.
	case err != nil:
		return err
	case !k.Silindi:
		return nil
	}
	if w.silici == nil {
		return nil
	}
	if _, err := w.silici.DeletePrefix(ctx, onek); err != nil {
		return fmt.Errorf("sosyal dosyalari silinemedi: %w", err)
	}
	return nil
}
