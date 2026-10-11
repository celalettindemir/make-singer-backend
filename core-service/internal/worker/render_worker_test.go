package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

type sahteSuno struct {
	uretHata, bekleHata, bolHata error
	track                        *client.Track
	stems                        []client.Stem
	uretimIstegi                 *client.GenerateMusicRequest
	uretimSayisi                 int
	stemBekleHata                error
	bolArgs                      [2]string
	bolSayisi                    int

	// focus modu
	ayirArgs       [3]string
	ayirSayisi     int
	ayirHata       error
	ayrik          *client.IsolatedStem
	ayrikBekleHata error
}

func (s *sahteSuno) GenerateMusic(_ context.Context, r *client.GenerateMusicRequest) (string, error) {
	s.uretimSayisi++
	s.uretimIstegi = r
	return "t1", s.uretHata
}
func (s *sahteSuno) WaitMusic(context.Context, string, time.Duration, time.Duration) (*client.Track, error) {
	return s.track, s.bekleHata
}
func (s *sahteSuno) SplitStems(_ context.Context, taskID, audioID string) (string, error) {
	s.bolSayisi++
	s.bolArgs = [2]string{taskID, audioID}
	return "s1", s.bolHata
}
func (s *sahteSuno) WaitStems(context.Context, string, time.Duration, time.Duration) ([]client.Stem, error) {
	return s.stems, s.stemBekleHata
}
func (s *sahteSuno) IsolateStem(_ context.Context, taskID, audioID, stemName string) (string, error) {
	s.ayirSayisi++
	s.ayirArgs = [3]string{taskID, audioID, stemName}
	return "i1", s.ayirHata
}
func (s *sahteSuno) WaitIsolated(context.Context, string, time.Duration, time.Duration) (*client.IsolatedStem, error) {
	return s.ayrik, s.ayrikBekleHata
}
func (s *sahteSuno) IsConfigured() bool { return true }

type yukleme struct {
	key, tip string
	veri     []byte
}

type sahteDepo struct{ yuklemeler []yukleme }

func (d *sahteDepo) Upload(_ context.Context, key string, body io.Reader, tip string) (string, time.Time, error) {
	b, _ := io.ReadAll(body)
	d.yuklemeler = append(d.yuklemeler, yukleme{key, tip, b})
	return "https://depo.test/" + key, time.Time{}, nil
}
func (d *sahteDepo) Delete(context.Context, string) error { return nil }
func (d *sahteDepo) GetSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (d *sahteDepo) URLFor(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func sesSunucusu(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/yok.mp3" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ses:" + r.URL.Path))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testPayload() *model.RenderJobPayload {
	return &model.RenderJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", StemMode: model.StemModeStems, Brief: model.Brief{Genre: model.Genre("pop"), Vibes: []string{"x"}}}
}

func testWorker(s *sahteSuno, d client.StorageClient) *RenderWorker {
	w := NewRenderWorker(nil, s, d, nil)
	w.pollAraligi = time.Millisecond
	return w
}

func hic(int, string) {}

func TestSunoIleUret_MutluYol(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{
		track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 180},
		stems: []client.Stem{
			{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"},
			{Instrument: model.InstrumentPiano, URL: srv.URL + "/piyano.mp3"},
		},
	}
	d := &sahteDepo{}

	res, err := testWorker(s, d).sunoIleUret(context.Background(), testPayload(), hic)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if !s.uretimIstegi.Instrumental || !strings.HasPrefix(s.uretimIstegi.Style, "pop, instrumental") {
		t.Errorf("uretim istegi = %+v", s.uretimIstegi)
	}
	if s.bolArgs != [2]string{"t1", "a1"} {
		t.Errorf("SplitStems argumanlari = %v", s.bolArgs)
	}
	if res.Duration != 180 || len(res.Stems) != 2 {
		t.Fatalf("sonuc = %+v", res)
	}
	if len(d.yuklemeler) != 2 {
		t.Fatalf("yukleme sayisi = %d, 2 bekleniyordu", len(d.yuklemeler))
	}
	for i, y := range d.yuklemeler {
		st := res.Stems[i]
		if y.key != st.FileKey || !strings.HasPrefix(y.key, "stems/kullanici-a/p1/") || !strings.HasSuffix(y.key, ".mp3") {
			t.Errorf("anahtar = %q, kayit %q", y.key, st.FileKey)
		}
		if y.tip != "audio/mpeg" {
			t.Errorf("icerik tipi = %q", y.tip)
		}
		if st.Duration != 180 {
			t.Errorf("stem suresi = %v", st.Duration)
		}
	}
	if !bytes.Equal(d.yuklemeler[0].veri, []byte("ses:/davul.mp3")) || res.Stems[1].Instrument != model.InstrumentPiano {
		t.Errorf("yuklenen icerik/enstruman yanlis: %q, %v", d.yuklemeler[0].veri, res.Stems[1].Instrument)
	}
}

func TestSunoIleUret_UretimReddiTekrarDenenebilir(t *testing.T) {
	s := &sahteSuno{uretHata: errors.New("kredi yok")}
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), testPayload(), hic)
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ucret oncesi hata tekrar denenebilir olmali, geldi: %v", err)
	}
}

func TestSunoIleUret_UcretliAdimSonrasiTekrarYok(t *testing.T) {
	srv := sesSunucusu(t)
	durumlar := map[string]*sahteSuno{
		"bekleme hatasi":      {bekleHata: errors.New("zaman asimi")},
		"bolme hatasi":        {track: &client.Track{ID: "a1"}, bolHata: errors.New("reddedildi")},
		"stem bekleme hatasi": {track: &client.Track{ID: "a1"}, stemBekleHata: errors.New("zaman asimi")},
		"stem yok":            {track: &client.Track{ID: "a1"}},
		"indirme 404": {track: &client.Track{ID: "a1"}, stems: []client.Stem{
			{Instrument: model.InstrumentDrums, URL: srv.URL + "/yok.mp3"},
		}},
	}
	for ad, s := range durumlar {
		t.Run(ad, func(t *testing.T) {
			d := &sahteDepo{}
			_, err := testWorker(s, d).sunoIleUret(context.Background(), testPayload(), hic)
			if !errors.Is(err, asynq.SkipRetry) {
				t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
			}
			if len(d.yuklemeler) != 0 {
				t.Errorf("basarisiz iste yukleme olmamali: %d", len(d.yuklemeler))
			}
		})
	}
}

func TestSunoIleUret_DepoYoksaSunoCagrilmaz(t *testing.T) {
	s := &sahteSuno{}
	_, err := testWorker(s, nil).sunoIleUret(context.Background(), testPayload(), hic)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
	if s.uretimSayisi != 0 {
		t.Errorf("depo yokken Suno %d kez cagrildi", s.uretimSayisi)
	}
}

func TestSunoIleUret_SureYoksaTahmin(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{
		track: &client.Track{ID: "a1", Duration: 0},
		stems: []client.Stem{{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"}},
	}
	p := testPayload()
	bpm := 120
	p.Brief.BPM.Value = &bpm
	p.Brief.Structure = []model.SongSection{{ID: "s1", Type: "verse", Bars: 8}}

	res, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if res.Duration != 16 || len(res.Stems) != 1 || res.Stems[0].Duration != 16 {
		t.Fatalf("sure = %v, stem = %+v; 16 bekleniyordu", res.Duration, res.Stems)
	}
}

func TestSunoIleUret_SahipsizIsteSunoCagrilmaz(t *testing.T) {
	s := &sahteSuno{}
	p := testPayload()
	p.OwnerID = ""
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
	if s.uretimSayisi != 0 {
		t.Errorf("sahipsiz iste Suno %d kez cagrildi", s.uretimSayisi)
	}
}

// panikleyenDepo, yuklemede panic eder (ornegin tipli nil bir R2 istemcisi).
type panikleyenDepo struct{ sahteDepo }

func (d *panikleyenDepo) Upload(context.Context, string, io.Reader, string) (string, time.Time, error) {
	panic("nil R2 istemcisi")
}

// TestSunoIleUret_UcretSonrasiPanicTekrarDenenmez: asynq panic'i yakalayip
// isi TEKRAR DENER; bu da Suno'yu yeniden ucretlendirir (canli testte 12
// kredi boyle bosa gitti). Ucretli adimdan sonraki panic SkipRetry olmali.
func TestSunoIleUret_UcretSonrasiPanicTekrarDenenmez(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{
		track: &client.Track{ID: "a1", Duration: 30},
		stems: []client.Stem{{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"}},
	}
	_, err := testWorker(s, &panikleyenDepo{}).sunoIleUret(context.Background(), testPayload(), hic)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
}

func TestSunoIleUret_MixModuAyirmaYapmaz(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 120}}
	d := &sahteDepo{}
	p := testPayload()
	p.StemMode = model.StemModeMix

	res, err := testWorker(s, d).sunoIleUret(context.Background(), p, hic)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if s.bolSayisi != 0 || s.ayirSayisi != 0 {
		t.Errorf("mix modunda ayirma cagrildi: split=%d advanced=%d", s.bolSayisi, s.ayirSayisi)
	}
	if res.StemMode != model.StemModeMix || len(res.Stems) != 1 || res.Stems[0].Instrument != model.InstrumentMix {
		t.Fatalf("sonuc = %+v", res)
	}
	if len(d.yuklemeler) != 1 || !bytes.Equal(d.yuklemeler[0].veri, []byte("ses:/tam.mp3")) || d.yuklemeler[0].tip != "audio/mpeg" {
		t.Errorf("yukleme = %+v", d.yuklemeler)
	}
	if !strings.HasPrefix(d.yuklemeler[0].key, "stems/kullanici-a/p1/") {
		t.Errorf("anahtar = %q", d.yuklemeler[0].key)
	}
}

func TestSunoIleUret_BosModMixGibi(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 120}}
	p := testPayload()
	p.StemMode = ""
	res, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if s.bolSayisi != 0 || res.StemMode != model.StemModeMix {
		t.Errorf("bos mod mix gibi islenmeli: split=%d mod=%q", s.bolSayisi, res.StemMode)
	}
}

func TestSunoIleUret_MixModuParcaAdresiYoksaTekrarYok(t *testing.T) {
	s := &sahteSuno{track: &client.Track{ID: "a1", Duration: 120}}
	p := testPayload()
	p.StemMode = model.StemModeMix
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
}

func TestSunoIleUret_FocusModuIkiStem(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{
		track: &client.Track{ID: "a1", Duration: 141},
		ayrik: &client.IsolatedStem{ExtractURL: srv.URL + "/davul.mp3", RemoveURL: srv.URL + "/kalan.mp3", Duration: 141},
	}
	d := &sahteDepo{}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	davul := model.InstrumentDrums
	p.FocusInstrument = &davul

	res, err := testWorker(s, d).sunoIleUret(context.Background(), p, hic)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if s.ayirArgs != [3]string{"t1", "a1", "Drum Kit"} || s.bolSayisi != 0 {
		t.Errorf("ayirma argumanlari = %v, split=%d", s.ayirArgs, s.bolSayisi)
	}
	if res.StemMode != model.StemModeFocus || len(res.Stems) != 2 {
		t.Fatalf("sonuc = %+v", res)
	}
	if res.Stems[0].Instrument != model.InstrumentDrums || res.Stems[1].Instrument != model.InstrumentRest {
		t.Errorf("enstrumanlar = %v, %v", res.Stems[0].Instrument, res.Stems[1].Instrument)
	}
	if len(d.yuklemeler) != 2 || !bytes.Equal(d.yuklemeler[1].veri, []byte("ses:/kalan.mp3")) {
		t.Errorf("yuklemeler = %+v", d.yuklemeler)
	}
}

func TestSunoIleUret_FocusEnstrumaniEslenmiyorsaSunoCagrilmaz(t *testing.T) {
	s := &sahteSuno{}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	p.FocusInstrument = nil
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if !errors.Is(err, asynq.SkipRetry) || s.uretimSayisi != 0 {
		t.Fatalf("ucret oncesi SkipRetry ve sifir uretim bekleniyordu: err=%v uretim=%d", err, s.uretimSayisi)
	}
}

func TestSunoIleUret_FocusAyirmaHatasiTekrarYok(t *testing.T) {
	s := &sahteSuno{track: &client.Track{ID: "a1"}, ayrikBekleHata: errors.New("katalog eslesmesi")}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	davul := model.InstrumentDrums
	p.FocusInstrument = &davul
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
}
