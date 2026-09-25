package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// sahteDepo, her URLFor cagrisinda FARKLI bir adres uretir. Boylece
// "saklanan adres mi donuyor, taze uretilen mi" sorusu kesin yanit alir.
type sahteDepo struct {
	cagri     int
	yuklenen  []string
	urlForCnt int
}

func (d *sahteDepo) Upload(ctx context.Context, key string, body io.Reader, contentType string) (string, time.Time, error) {
	d.yuklenen = append(d.yuklenen, key)
	u, e, err := d.URLFor(ctx, key)
	return u, e, err
}

func (d *sahteDepo) Delete(ctx context.Context, key string) error { return nil }

func (d *sahteDepo) GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return "", nil
}

func (d *sahteDepo) URLFor(ctx context.Context, key string) (string, time.Time, error) {
	d.cagri++
	d.urlForCnt++
	return fmt.Sprintf("https://makeasinger-private.hesap123.r2.cloudflarestorage.com/%s?X-Amz-Signature=taze%d", key, d.cagri),
		time.Now().Add(time.Hour), nil
}

func testR2Cfg() *config.R2Config {
	return &config.R2Config{
		AccountID:     "hesap123",
		PublicBucket:  "makeasinger-public",
		PrivateBucket: "makeasinger-private",
		PublicURL:     "https://makesinger-cdn.celalettindemir.dev",
		PresignTTL:    time.Hour,
	}
}

// eskiKayit, hatali surumun Redis'e yazdigi kaydi taklit eder: icinde
// donmus bir imzali URL ve GECMISTE kalan bir expiresAt vardir. Yeni
// kod fileKey'i okuyup adresi yeniden uretmeli, bu olu adresi degil.
func eskiKayitBytes(t *testing.T) []byte {
	t.Helper()
	gecmis := time.Now().Add(-3 * time.Hour)
	ham := map[string]any{
		"fileKey":   "masters/p1/m1.wav",
		"fileUrl":   "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/p1/m1.wav?X-Amz-Signature=DONMUS",
		"expiresAt": gecmis.Format(time.RFC3339Nano),
		"duration":  180.5,
		"profile":   "clean",
		"peakDb":    -0.3,
		"lufs":      -14,
	}
	b, err := json.Marshal(ham)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestMasterService_BuildResult_AdresHerOkumadaTazelenir(t *testing.T) {
	depo := &sahteDepo{}
	s := NewMasterService(nil, nil, depo, testR2Cfg())

	resp, err := s.BuildResult(context.Background(), eskiKayitBytes(t))
	if err != nil {
		t.Fatalf("BuildResult: %v", err)
	}

	if resp.FileURL == "" || resp.FileURL == "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/p1/m1.wav?X-Amz-Signature=DONMUS" {
		t.Fatalf("saklanan donmus adres dondu: %q", resp.FileURL)
	}
	if resp.ExpiresAt == nil {
		t.Fatal("ExpiresAt nil")
	}
	if !resp.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt gecmiste: %v (saklanan deger dondurulmus olabilir)", resp.ExpiresAt)
	}
	if resp.Duration != 180.5 || resp.LUFS != -14 {
		t.Errorf("olcumler kayboldu: %+v", resp)
	}

	// Ikinci okuma yine YENI bir adres uretmeli.
	resp2, err := s.BuildResult(context.Background(), eskiKayitBytes(t))
	if err != nil {
		t.Fatalf("ikinci BuildResult: %v", err)
	}
	if resp2.FileURL == resp.FileURL {
		t.Errorf("ikinci okumada ayni adres dondu, imzalama tekrarlanmamis: %q", resp2.FileURL)
	}
}

func TestMasterService_BuildResult_AnahtarsizKayitHata(t *testing.T) {
	s := NewMasterService(nil, nil, &sahteDepo{}, testR2Cfg())
	if _, err := s.BuildResult(context.Background(), []byte(`{"duration":1}`)); err == nil {
		t.Error("anahtarsiz kayit icin hata bekleniyordu")
	}
}

func TestRenderService_BuildResult_StemAdresleriTazelenir(t *testing.T) {
	depo := &sahteDepo{}
	s := NewRenderService(nil, nil, depo, testR2Cfg())

	record := &model.RenderResultRecord{
		ID:  "r1",
		BPM: 120,
		Stems: []model.StemResultRecord{
			{ID: "s1", Instrument: model.Instrument("drums"), FileKey: "stems/p1/s1.wav", Duration: 10},
		},
	}

	resp, err := s.BuildResult(context.Background(), record)
	if err != nil {
		t.Fatalf("BuildResult: %v", err)
	}
	if len(resp.Stems) != 1 {
		t.Fatalf("stem sayisi = %d", len(resp.Stems))
	}
	if resp.Stems[0].FileURL == "" {
		t.Fatal("stem adresi bos")
	}
	if resp.Stems[0].ExpiresAt == nil || !resp.Stems[0].ExpiresAt.After(time.Now()) {
		t.Errorf("stem expiresAt taze degil: %v", resp.Stems[0].ExpiresAt)
	}

	resp2, err := s.BuildResult(context.Background(), record)
	if err != nil {
		t.Fatalf("ikinci BuildResult: %v", err)
	}
	if resp2.Stems[0].FileURL == resp.Stems[0].FileURL {
		t.Errorf("stem adresi tekrar uretilmemis: %q", resp2.Stems[0].FileURL)
	}
}

// TestRenderService_BuildResult_SaklananJSONdaImzaliURLYok: kayit
// serilestirildiginde icinde imzali adres BULUNMAMALI.
func TestRenderService_BuildResult_KayittaImzaYok(t *testing.T) {
	record := &model.RenderResultRecord{
		Stems: []model.StemResultRecord{{ID: "s1", FileKey: "stems/p1/s1.wav"}},
	}
	b, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); strings.Contains(got, "X-Amz-Signature") || strings.Contains(got, "fileUrl") {
		t.Errorf("kayitta imzali adres var: %s", got)
	}
}

// TestUploadService_TekImzalama: Upload zaten adresi donduruyor; ayrica
// URLFor cagrilmasi ikinci (bosa giden) bir imzalama demekti.
func TestUploadService_TekImzalama(t *testing.T) {
	depo := &sahteDepo{}
	s := NewUploadService(depo, &config.R2Config{})

	resp, err := s.UploadVocal(context.Background(), "p1", "s1", "take1", nil, 0)
	if err != nil {
		t.Fatalf("UploadVocal: %v", err)
	}
	if resp.FileURL == "" {
		t.Fatal("adres bos")
	}
	if depo.urlForCnt != 1 {
		t.Errorf("URLFor %d kez cagrildi, 1 bekleniyordu (Upload icindeki tek imzalama)", depo.urlForCnt)
	}
	if resp.ExpiresAt == nil {
		t.Error("private vokal icin expiresAt bekleniyordu")
	}
}

// TestUploadService_MockAdresi_KeyFromURLIleCozulur, r2Client
// yapilandirilmamisken (mock yol) UploadVocal'in urettigi adresin
// KeyFromURL ile geri anahtara cozulebildigini dogrular. Eskiden bu
// adres sabit "https://cdn.makeasinger.com/..." idi ve bilinmeyen bir
// konak oldugu icin sonraki bir master isteginde KeyFromURL hata
// veriyordu ("taninmayan konak").
func TestUploadService_MockAdresi_KeyFromURLIleCozulur(t *testing.T) {
	r2Cfg := &config.R2Config{
		PrivateBucket: "makeasinger-private",
		AccountID:     "hesap123",
	}
	s := NewUploadService(nil, r2Cfg)

	resp, err := s.UploadVocal(context.Background(), "p1", "s1", "take1", nil, 0)
	if err != nil {
		t.Fatalf("UploadVocal: %v", err)
	}

	beklenenAnahtar := fmt.Sprintf("vocals/p1/%s.wav", resp.ID)
	gotKey, err := client.KeyFromURL(resp.FileURL, r2Cfg)
	if err != nil {
		t.Fatalf("mock adresi KeyFromURL ile cozulemedi: %v (adres: %s)", err, resp.FileURL)
	}
	if gotKey != beklenenAnahtar {
		t.Errorf("cozulen anahtar = %q, beklenen %q", gotKey, beklenenAnahtar)
	}
}

// TestUploadService_GetSignedURLMock_KeyFromURLIleCozulur,
// GetSignedURL'in mock yolunun da ayni kurallarla cozulebilen bir adres
// urettigini dogrular.
func TestUploadService_GetSignedURLMock_KeyFromURLIleCozulur(t *testing.T) {
	r2Cfg := &config.R2Config{
		PrivateBucket: "makeasinger-private",
		AccountID:     "hesap123",
	}
	s := NewUploadService(nil, r2Cfg)

	anahtar := "vocals/p1/take1.wav"
	url, err := s.GetSignedURL(context.Background(), anahtar, time.Hour)
	if err != nil {
		t.Fatalf("GetSignedURL: %v", err)
	}

	gotKey, err := client.KeyFromURL(url, r2Cfg)
	if err != nil {
		t.Fatalf("mock adresi KeyFromURL ile cozulemedi: %v (adres: %s)", err, url)
	}
	if gotKey != anahtar {
		t.Errorf("cozulen anahtar = %q, beklenen %q", gotKey, anahtar)
	}
}
