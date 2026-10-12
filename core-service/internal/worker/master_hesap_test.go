package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

// sahteSes, master cagrisini sayar. Gercek ses servisi ciktiyi R2'ye
// kendisi yazar; bu yuzden Master cagrisi master isinin R2 yuklemesidir.
type sahteSes struct {
	masterSayisi int
	hata         error // nil ise yine hata doner (bkz. Master)
}

func (s *sahteSes) Master(context.Context, *client.MasterRequest) (*client.MasterResponse, error) {
	s.masterSayisi++
	if s.hata == nil {
		// Basari yolu hub yayinina girer (testte hub yok); cagri sayilip
		// hata ile donulur.
		return nil, errors.New("sahte ses: master cagrildi")
	}
	return nil, s.hata
}
func (s *sahteSes) Encode(context.Context, *client.EncodeRequest) (*client.EncodeResponse, error) {
	return nil, nil
}
func (s *sahteSes) CreateZip(context.Context, *client.ZipRequest) (*client.ZipResponse, error) {
	return nil, nil
}
func (s *sahteSes) HealthCheck(context.Context) error { return nil }

// masterTestWorker: is kaydi Redis'i ulasilamaz; getJob hata verir ve
// durum guncellemeleri sessizce atlanir (hub'a hic dokunulmaz).
func masterTestWorker(ses *sahteSes, h *sahteDenetci) *MasterWorker {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	// Tipli nil tuzagina dusmemek icin nil isaretci arayuze atanmaz.
	var d deletionmark.Checker
	if h != nil {
		d = h
	}
	return NewMasterWorker(rdb, ses, &sahteDepo{}, nil, nil, testR2Cfg(), d)
}

func masterPayload() *model.MasterJobPayload {
	return &model.MasterJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", Profile: model.MasterProfile("clean")}
}

func TestMasterWorker_SilinmisSahipIcinYuklemeYok(t *testing.T) {
	ses := &sahteSes{}
	h := &sahteDenetci{yanit: hepSilinmis}
	err := masterTestWorker(ses, h).processWithAudioService(context.Background(), "j1", masterPayload())
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
	if ses.masterSayisi != 0 {
		t.Fatalf("silinmis sahip icin master %d kez cagrildi", ses.masterSayisi)
	}
	if len(h.sorulan) == 0 || h.sorulan[0] != "kullanici-a" {
		t.Fatalf("is sahibi denetlenmedi: %v", h.sorulan)
	}
}

func TestMasterWorker_DenetimHatasindaTekrarDenenir(t *testing.T) {
	ses := &sahteSes{}
	h := &sahteDenetci{yanit: func(int) (bool, error) { return false, errors.New("redis kapali") }}
	err := masterTestWorker(ses, h).processWithAudioService(context.Background(), "j1", masterPayload())
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("denetim hatasi tekrar denenebilir olmali, geldi: %v", err)
	}
	if ses.masterSayisi != 0 {
		t.Fatalf("denetim hatasinda master %d kez cagrildi", ses.masterSayisi)
	}
}

func TestMasterWorker_SilinmemisSahipteMasterCagrilir(t *testing.T) {
	ses := &sahteSes{}
	h := &sahteDenetci{yanit: func(int) (bool, error) { return false, nil }}
	_ = masterTestWorker(ses, h).processWithAudioService(context.Background(), "j1", masterPayload())
	if ses.masterSayisi != 1 {
		t.Fatalf("master %d kez cagrildi, 1 bekleniyordu", ses.masterSayisi)
	}
}

func TestMasterWorker_DenetciYoksaDenetimYapilmaz(t *testing.T) {
	ses := &sahteSes{}
	_ = masterTestWorker(ses, nil).processWithAudioService(context.Background(), "j1", masterPayload())
	if ses.masterSayisi != 1 {
		t.Fatalf("master %d kez cagrildi, 1 bekleniyordu", ses.masterSayisi)
	}
}
