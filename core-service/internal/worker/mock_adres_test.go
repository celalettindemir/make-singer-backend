package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

func testR2Cfg() *config.R2Config {
	return &config.R2Config{
		AccountID:     "hesap123",
		PublicBucket:  "makeasinger-public",
		PrivateBucket: "makeasinger-private",
		PublicURL:     "https://makesinger-cdn.celalettindemir.dev",
	}
}

// TestMasterWorker_MockCiktisiCozulebilir: mock/dev yolunun urettigi adres
// istemciden geri geldiginde KeyFromURL ile cozulebilmeli. Eski sabit
// "https://cdn.makeasinger.com/masters/..." adresi cozulemiyor ve opak bir
// 500 uretiyordu.
func TestMasterWorker_MockCiktisiCozulebilir(t *testing.T) {
	cfg := testR2Cfg()
	w := &MasterWorker{r2Cfg: cfg}

	record := w.generateMockRecord(&model.MasterJobPayload{ProjectID: "p1"})
	if record.FileKey == "" {
		t.Fatal("mock kayitta nesne anahtari yok")
	}
	if !strings.HasPrefix(record.FileKey, "masters/") {
		t.Errorf("mock anahtar = %q, masters/ onekli olmali", record.FileKey)
	}

	url, _, err := client.ResolveURL(context.Background(), nil, cfg, record.FileKey)
	if err != nil {
		t.Fatalf("ResolveURL: %v", err)
	}
	got, err := client.KeyFromURL(url, cfg)
	if err != nil {
		t.Fatalf("mock adres cozulemedi (%q): %v", url, err)
	}
	if got != record.FileKey {
		t.Errorf("gidis donus bozuk: %q -> %q", record.FileKey, got)
	}
}

// TestRenderWorker_MockStemAdresiCozulebilir: ayni kural stem'ler icin.
func TestRenderWorker_MockStemAdresiCozulebilir(t *testing.T) {
	cfg := testR2Cfg()
	w := &RenderWorker{}

	payload := &model.RenderJobPayload{ProjectID: "p1"}
	payload.Arrangement.Instruments = []model.Instrument{model.Instrument("drums")}

	record := w.generateMockResult(payload)
	if len(record.Stems) != 1 {
		t.Fatalf("stem sayisi = %d", len(record.Stems))
	}
	key := record.Stems[0].FileKey
	if !strings.HasPrefix(key, "stems/") {
		t.Fatalf("mock stem anahtari = %q", key)
	}

	url, _, err := client.ResolveURL(context.Background(), nil, cfg, key)
	if err != nil {
		t.Fatalf("ResolveURL: %v", err)
	}
	if got, err := client.KeyFromURL(url, cfg); err != nil || got != key {
		t.Errorf("mock stem adresi cozulemedi: url=%q got=%q err=%v", url, got, err)
	}
}

// TestBuildMixSettings_AdresTasimaz: mix kanallari artik hicbir adres
// alani tasimaz; imzali URL Python istek govdesine ve loglara dusmez.
func TestBuildMixSettings_AdresTasimaz(t *testing.T) {
	w := &MasterWorker{r2Cfg: testR2Cfg()}
	imzali := "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/stems/p1/s1.wav?X-Amz-Signature=GIZLI"

	payload := &model.MasterJobPayload{StemURLs: []string{imzali}}
	settings := w.buildMixSettings(payload)
	if len(settings) != 1 {
		t.Fatalf("kanal sayisi = %d", len(settings))
	}

	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	govde := string(b)
	if strings.Contains(govde, "X-Amz-Signature") || strings.Contains(govde, "stem_url") || strings.Contains(govde, "GIZLI") {
		t.Errorf("mix ayarlarinda adres/imza var: %s", govde)
	}
}
