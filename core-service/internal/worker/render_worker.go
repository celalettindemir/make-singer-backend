package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/websocket"
)

// stemBoyutTavani: tek stem dosyasi icin indirme siniri.
const stemBoyutTavani = 50 << 20

// RenderWorker processes render jobs
type RenderWorker struct {
	renderService *service.RenderService
	music         client.MusicGenerator
	r2Client      client.StorageClient
	hub           *websocket.Hub
	indirici      *http.Client
	pollAraligi   time.Duration
}

// NewRenderWorker creates a new render worker
func NewRenderWorker(renderService *service.RenderService, music client.MusicGenerator, r2Client client.StorageClient, hub *websocket.Hub) *RenderWorker {
	return &RenderWorker{
		renderService: renderService,
		music:         music,
		r2Client:      r2Client,
		hub:           hub,
		indirici:      &http.Client{Timeout: 30 * time.Second},
		pollAraligi:   5 * time.Second,
	}
}

// ProcessTask handles render task processing
func (w *RenderWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var taskPayload struct {
		JobID   string          `json:"jobId"`
		Payload json.RawMessage `json:"payload"`
	}

	if err := json.Unmarshal(t.Payload(), &taskPayload); err != nil {
		return fmt.Errorf("failed to unmarshal task payload: %w", err)
	}

	jobID := taskPayload.JobID
	log.Printf("Starting render job: %s", jobID)

	var payload model.RenderJobPayload
	if err := json.Unmarshal(taskPayload.Payload, &payload); err != nil {
		w.failJob(ctx, jobID, "Invalid payload")
		return fmt.Errorf("failed to unmarshal render payload: %w", err)
	}

	// Check if Suno client is configured
	if w.music == nil || !w.music.IsConfigured() {
		return w.processWithMock(ctx, jobID, &payload)
	}

	return w.processWithSuno(ctx, jobID, &payload)
}

// processWithSuno handles real rendering using Suno API
func (w *RenderWorker) processWithSuno(ctx context.Context, jobID string, payload *model.RenderJobPayload) error {
	result, err := w.sunoIleUret(ctx, payload, func(p int, adim string) {
		w.updateProgress(ctx, jobID, p, adim)
	})
	if err != nil {
		w.failJob(ctx, jobID, err.Error())
		return err
	}

	if err := w.renderService.CompleteJob(ctx, jobID, result); err != nil {
		w.failJob(ctx, jobID, "Failed to save result")
		// Suno ucreti odendi; tekrar deneme isi bastan calistirip yeniden ucretlendirir.
		return fmt.Errorf("sonuc kaydedilemedi: %w: %w", err, asynq.SkipRetry)
	}

	// Yayin icin adresler taze uretilir; kayitta anahtar durur.
	resp, err := w.renderService.BuildResult(ctx, result)
	if err != nil {
		w.failJob(ctx, jobID, fmt.Sprintf("Stem URL uretilemedi: %v", err))
		// Ayni neden: ucretli uretim bitti, tekrar deneme yeniden ucretlendirir.
		return fmt.Errorf("stem URL uretilemedi: %w: %w", err, asynq.SkipRetry)
	}
	w.hub.BroadcastComplete(jobID, resp)
	log.Printf("Render job %s completed", jobID)
	return nil
}

// sunoIleUret, Suno'da uretip stem'leri R2'ye aktarir. renderService ve
// hub'a dokunmaz; ilerleme geri cagrimla bildirilir.
//
// Tekrar deneme: GenerateMusic basarili olunca kredi harcanmistir. Bundan
// sonraki her hata asynq.SkipRetry ile sarilir; aksi halde asynq isi bastan
// calistirip Suno'yu yeniden ucretlendirir (MaxRetry(3) ile 4 x 62 kredi).
func (w *RenderWorker) sunoIleUret(ctx context.Context, payload *model.RenderJobPayload, ilerleme func(int, string)) (sonuc *model.RenderResultRecord, hata error) {
	if w.r2Client == nil {
		return nil, fmt.Errorf("depolama yapilandirilmamis, Suno cagrilmadi: %w", asynq.SkipRetry)
	}

	// Ucretli cagridan ONCE: sahip/proje anahtar kurmaya uygun degilse
	// stem'ler yuklenemez ve 62 kredi bosa gider.
	if _, err := client.KullaniciAnahtari("stems", payload.OwnerID, payload.ProjectID, "x.mp3"); err != nil {
		return nil, fmt.Errorf("is sahibi/proje anahtara uygun degil, Suno cagrilmadi: %w: %w", err, asynq.SkipRetry)
	}

	ilerleme(5, "Building music prompt...")
	req := &client.GenerateMusicRequest{
		Style:        buildMusicStyle(payload),
		Title:        fmt.Sprintf("%s instrumental", payload.Brief.Genre),
		Instrumental: true,
	}

	ilerleme(10, "Generating music...")
	taskID, err := w.music.GenerateMusic(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("music generation failed: %w", err)
	}
	ucretli := func(ne string, err error) error {
		return fmt.Errorf("%s: %w: %w", ne, err, asynq.SkipRetry)
	}
	// Panic de bir hatadir: asynq onu yakalayip isi TEKRAR DENER ve Suno
	// yeniden ucretlendirilir. Ucret odendikten sonraki panic SkipRetry'a
	// cevrilir.
	defer func() {
		if r := recover(); r != nil {
			sonuc = nil
			hata = ucretli("render panic", fmt.Errorf("%v", r))
		}
	}()

	ilerleme(30, "Waiting for music generation...")
	track, err := w.music.WaitMusic(ctx, taskID, w.pollAraligi, 10*time.Minute)
	if err != nil {
		return nil, ucretli("music generation failed", err)
	}

	ilerleme(60, "Splitting stems...")
	stemTaskID, err := w.music.SplitStems(ctx, taskID, track.ID)
	if err != nil {
		return nil, ucretli("stem splitting failed", err)
	}

	ilerleme(75, "Waiting for stem separation...")
	stems, err := w.music.WaitStems(ctx, stemTaskID, w.pollAraligi, 5*time.Minute)
	if err != nil {
		return nil, ucretli("stem splitting failed", err)
	}
	if len(stems) == 0 {
		return nil, ucretli("stem splitting failed", errors.New("suno hic enstruman stem'i donmedi"))
	}

	// Suno sure donmezse yapidan tahmin edilir.
	sure := track.Duration
	if sure <= 0 {
		sure = tahminiSure(payload)
	}

	ilerleme(90, "Uploading stems...")
	records, err := w.uploadStems(ctx, payload.OwnerID, payload.ProjectID, stems, sure)
	if err != nil {
		return nil, ucretli("stem upload failed", err)
	}

	ilerleme(95, "Finalizing...")
	return w.generateResult(payload, sure, records), nil
}

// processWithMock handles rendering with mock data for development
func (w *RenderWorker) processWithMock(ctx context.Context, jobID string, payload *model.RenderJobPayload) error {
	steps := []struct {
		progress int
		step     string
		duration time.Duration
	}{
		{10, "Analyzing song structure...", 2 * time.Second},
		{20, "Generating drums track...", 3 * time.Second},
		{35, "Generating bass track...", 3 * time.Second},
		{50, "Generating piano track...", 3 * time.Second},
		{65, "Generating guitar track...", 3 * time.Second},
		{80, "Generating synth track...", 3 * time.Second},
		{90, "Mixing stems...", 2 * time.Second},
		{95, "Finalizing...", 1 * time.Second},
	}

	for _, step := range steps {
		select {
		case <-ctx.Done():
			log.Printf("Render job %s cancelled", jobID)
			return ctx.Err()
		default:
		}

		w.updateProgress(ctx, jobID, step.progress, step.step)
		time.Sleep(step.duration)
	}

	result, err := w.generateMockResult(payload)
	if err != nil {
		w.failJob(ctx, jobID, "Gecersiz is sahibi veya proje")
		return fmt.Errorf("mock sonuc: %w: %w", err, asynq.SkipRetry)
	}

	if err := w.renderService.CompleteJob(ctx, jobID, result); err != nil {
		w.failJob(ctx, jobID, "Failed to save result")
		return err
	}

	resp, err := w.renderService.BuildResult(ctx, result)
	if err != nil {
		w.failJob(ctx, jobID, fmt.Sprintf("Stem URL uretilemedi: %v", err))
		return err
	}
	w.hub.BroadcastComplete(jobID, resp)
	log.Printf("Render job %s completed (mock)", jobID)
	return nil
}

// uploadStems, Suno stem'lerini indirip R2'ye yazar. Kayda imzali URL degil
// ANAHTAR yazilir; adres okuma aninda (RenderService.GetResult) taze uretilir.
func (w *RenderWorker) uploadStems(ctx context.Context, ownerID, projectID string, stems []client.Stem, duration float64) ([]model.StemResultRecord, error) {
	results := make([]model.StemResultRecord, 0, len(stems))
	for _, stem := range stems {
		stemID := uuid.New().String()
		key, err := client.KullaniciAnahtari("stems", ownerID, projectID, stemID+".mp3")
		if err != nil {
			return nil, err
		}
		if err := w.stemAktar(ctx, stem.URL, key); err != nil {
			return nil, fmt.Errorf("%s stem'i aktarilamadi: %w", stem.Instrument, err)
		}
		results = append(results, model.StemResultRecord{
			ID:           stemID,
			Instrument:   stem.Instrument,
			FileKey:      key,
			Duration:     duration,
			WaveformData: generateWaveform(100),
		})
	}
	return results, nil
}

// stemAktar, adresi indirip anahtara yukler. Govde bellege alinir: S3
// PutObject icerik uzunlugu ister ve tavani asan dosya yarim yuklenmemeli.
func (w *RenderWorker) stemAktar(ctx context.Context, adres, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, adres, nil)
	if err != nil {
		return err
	}
	resp, err := w.indirici.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("indirme durumu %d", resp.StatusCode)
	}
	veri, err := io.ReadAll(io.LimitReader(resp.Body, stemBoyutTavani+1))
	if err != nil {
		return err
	}
	if len(veri) > stemBoyutTavani {
		return fmt.Errorf("stem %d bayt tavanini asiyor", stemBoyutTavani)
	}
	_, _, err = w.r2Client.Upload(ctx, key, bytes.NewReader(veri), "audio/mpeg")
	return err
}

func (w *RenderWorker) generateResult(payload *model.RenderJobPayload, duration float64, stems []model.StemResultRecord) *model.RenderResultRecord {
	tonic := model.TonicC
	scale := model.ScaleMajor
	if payload.Brief.Key.Tonic != nil {
		tonic = *payload.Brief.Key.Tonic
	}
	if payload.Brief.Key.Scale != nil {
		scale = *payload.Brief.Key.Scale
	}

	bpm := 120
	if payload.Brief.BPM.Value != nil {
		bpm = *payload.Brief.BPM.Value
	}

	return &model.RenderResultRecord{
		ID:        uuid.New().String(),
		BPM:       bpm,
		Duration:  duration,
		Key:       model.KeyResult{Tonic: tonic, Scale: scale},
		CreatedAt: time.Now(),
		Stems:     stems,
	}
}

func (w *RenderWorker) updateProgress(ctx context.Context, jobID string, progress int, step string) {
	if err := w.renderService.UpdateJobProgress(ctx, jobID, progress, step); err != nil {
		log.Printf("Failed to update progress: %v", err)
	}
	w.hub.BroadcastProgress(jobID, progress, model.JobStatusRunning, step)
}

func (w *RenderWorker) failJob(ctx context.Context, jobID, errMsg string) {
	if err := w.renderService.FailJob(ctx, jobID, errMsg); err != nil {
		log.Printf("Failed to mark job as failed: %v", err)
	}
	w.hub.BroadcastError(jobID, "RENDER_FAILED", errMsg)
}

func (w *RenderWorker) generateMockResult(payload *model.RenderJobPayload) (*model.RenderResultRecord, error) {
	bpm := 120
	if payload.Brief.BPM.Value != nil {
		bpm = *payload.Brief.BPM.Value
	}
	duration := tahminiSure(payload)

	tonic := model.TonicC
	scale := model.ScaleMajor
	if payload.Brief.Key.Tonic != nil {
		tonic = *payload.Brief.Key.Tonic
	}
	if payload.Brief.Key.Scale != nil {
		scale = *payload.Brief.Key.Scale
	}

	var stems []model.StemResultRecord
	for _, instrument := range payload.Arrangement.Instruments {
		stemID := uuid.New().String()
		key, err := client.KullaniciAnahtari("stems", payload.OwnerID, payload.ProjectID, string(instrument)+".wav")
		if err != nil {
			return nil, err
		}
		stems = append(stems, model.StemResultRecord{
			ID:           stemID,
			Instrument:   instrument,
			FileKey:      key,
			Duration:     duration,
			WaveformData: generateWaveform(100),
		})
	}

	return &model.RenderResultRecord{
		ID:        uuid.New().String(),
		BPM:       bpm,
		Duration:  duration,
		Key:       model.KeyResult{Tonic: tonic, Scale: scale},
		CreatedAt: time.Now(),
		Stems:     stems,
	}, nil
}

// tahminiSure, yapidaki toplam olcu x 4 vurus / BPM x 60 ile saniye tahmini.
func tahminiSure(payload *model.RenderJobPayload) float64 {
	var totalBars int
	for _, section := range payload.Brief.Structure {
		totalBars += section.Bars
	}
	bpm := 120
	if payload.Brief.BPM.Value != nil {
		bpm = *payload.Brief.BPM.Value
	}
	return float64(totalBars*4) / float64(bpm) * 60
}

func generateWaveform(points int) []float64 {
	waveform := make([]float64, points)
	for i := range waveform {
		waveform[i] = 0.1 + float64(i%10)/15.0
	}
	return waveform
}
