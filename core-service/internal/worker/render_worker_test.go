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
	s.bolArgs = [2]string{taskID, audioID}
	return "s1", s.bolHata
}
func (s *sahteSuno) WaitStems(context.Context, string, time.Duration, time.Duration) ([]client.Stem, error) {
	return s.stems, s.stemBekleHata
}
func (s *sahteSuno) IsolateStem(_ context.Context, taskID, audioID, stemName string) (string, error) {
	return "i1", nil
}
func (s *sahteSuno) WaitIsolated(context.Context, string, time.Duration, time.Duration) (*client.IsolatedStem, error) {
	return nil, nil
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
	return &model.RenderJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", Brief: model.Brief{Genre: model.Genre("pop"), Vibes: []string{"x"}}}
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
