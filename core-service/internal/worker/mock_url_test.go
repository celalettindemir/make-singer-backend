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

// TestMasterWorker_MockOutputResolvable: the URL produced by the mock/dev
// path must be resolvable with KeyFromURL when it comes back from the
// client. The old fixed "https://cdn.makeasinger.com/masters/..." URL could
// not be resolved and produced an opaque 500.
func TestMasterWorker_MockOutputResolvable(t *testing.T) {
	cfg := testR2Cfg()
	w := &MasterWorker{r2Cfg: cfg}

	record, err := w.generateMockRecord(&model.MasterJobPayload{OwnerID: "kullanici-a", ProjectID: "p1"})
	if err != nil {
		t.Fatalf("generateMockRecord: %v", err)
	}
	if record.FileKey == "" {
		t.Fatal("mock kayitta nesne anahtari yok")
	}
	if !strings.HasPrefix(record.FileKey, "masters/kullanici-a/p1/") {
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

// TestRenderWorker_MockStemURLResolvable: the same rule for stems.
func TestRenderWorker_MockStemURLResolvable(t *testing.T) {
	cfg := testR2Cfg()
	w := &RenderWorker{}

	payload := &model.RenderJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", StemMode: model.StemModeStems}
	payload.Arrangement.Instruments = []model.Instrument{model.Instrument("drums")}

	record, err := w.generateMockResult(payload)
	if err != nil {
		t.Fatalf("generateMockResult: %v", err)
	}
	if len(record.Stems) != 1 {
		t.Fatalf("stem sayisi = %d", len(record.Stems))
	}
	key := record.Stems[0].FileKey
	if !strings.HasPrefix(key, "stems/kullanici-a/p1/") {
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

// TestMasterWorker_BuildMixSettingsCarriesNoURL: mix channels no longer carry
// any URL field; a signed URL does not reach the Python request body or logs.
func TestMasterWorker_BuildMixSettingsCarriesNoURL(t *testing.T) {
	w := &MasterWorker{r2Cfg: testR2Cfg()}
	signed := "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/stems/p1/s1.wav?X-Amz-Signature=GIZLI"

	payload := &model.MasterJobPayload{StemURLs: []string{signed}}
	settings := w.buildMixSettings(payload)
	if len(settings) != 1 {
		t.Fatalf("kanal sayisi = %d", len(settings))
	}

	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(b)
	if strings.Contains(body, "X-Amz-Signature") || strings.Contains(body, "stem_url") || strings.Contains(body, "GIZLI") {
		t.Errorf("mix ayarlarinda adres/imza var: %s", body)
	}
}

func TestRenderWorker_MockModes(t *testing.T) {
	w := &RenderWorker{}
	drums := model.InstrumentDrums
	cases := []struct {
		mode  model.StemMode
		focus *model.Instrument
		want  []model.Instrument
	}{
		{model.StemModeMix, nil, []model.Instrument{model.InstrumentMix}},
		{"", nil, []model.Instrument{model.InstrumentMix}},
		{model.StemModeFocus, &drums, []model.Instrument{model.InstrumentDrums, model.InstrumentRest}},
		{model.StemModeStems, nil, []model.Instrument{model.InstrumentDrums, model.InstrumentBass}},
	}
	for _, d := range cases {
		p := &model.RenderJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", StemMode: d.mode, FocusInstrument: d.focus}
		p.Arrangement.Instruments = []model.Instrument{model.InstrumentDrums, model.InstrumentBass}
		rec, err := w.generateMockResult(p)
		if err != nil {
			t.Fatalf("%q: %v", d.mode, err)
		}
		var got []model.Instrument
		for _, s := range rec.Stems {
			got = append(got, s.Instrument)
		}
		if len(got) != len(d.want) {
			t.Fatalf("%q: stemler = %v, beklenen %v", d.mode, got, d.want)
		}
		for i := range got {
			if got[i] != d.want[i] {
				t.Errorf("%q: stemler = %v, beklenen %v", d.mode, got, d.want)
			}
		}
	}
}
