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
	return s.stems, nil
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
	return &model.RenderJobPayload{ProjectID: "p1", Brief: model.Brief{Genre: model.Genre("pop"), Vibes: []string{"x"}}}
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
	for i, y := range d.yuklemeler {
		st := res.Stems[i]
		if y.key != st.FileKey || !strings.HasPrefix(y.key, "stems/p1/") || !strings.HasSuffix(y.key, ".mp3") {
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
		"bekleme hatasi": {bekleHata: errors.New("zaman asimi")},
		"bolme hatasi":   {track: &client.Track{ID: "a1"}, bolHata: errors.New("reddedildi")},
		"stem yok":       {track: &client.Track{ID: "a1"}},
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
