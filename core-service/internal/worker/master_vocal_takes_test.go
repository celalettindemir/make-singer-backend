package worker

import (
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

func TestBuildVocalTakes_PassesTimeline(t *testing.T) {
	cfg := testR2Cfg()
	start, end := 1000, 2500
	w := &MasterWorker{r2Cfg: cfg}
	payload := &model.MasterJobPayload{
		OwnerID: "kullanici-a",
		VocalTakes: []model.VocalTake{
			{FileURL: client.UnsignedURL("vocals/kullanici-a/p1/s1/t1.wav", cfg), StartMs: &start, EndMs: &end},
			{FileURL: client.UnsignedURL("vocals/kullanici-a/p1/s2/t1.wav", cfg)},
		},
	}

	takes, err := w.buildVocalTakes(payload)
	if err != nil {
		t.Fatalf("buildVocalTakes: %v", err)
	}
	if len(takes) != 2 {
		t.Fatalf("takes = %d, want 2", len(takes))
	}
	if takes[0].StartMs == nil || *takes[0].StartMs != 1000 || takes[0].EndMs == nil || *takes[0].EndMs != 2500 {
		t.Errorf("take 0 = %+v, want start 1000 end 2500", takes[0])
	}
	if takes[1].StartMs != nil || takes[1].EndMs != nil {
		t.Errorf("take 1 = %+v, want nil timeline", takes[1])
	}
}

func TestMaster_TakeGainPassedThrough(t *testing.T) {
	cfg := testR2Cfg()
	gain := -6.0
	w := &MasterWorker{r2Cfg: cfg}
	payload := &model.MasterJobPayload{
		OwnerID: "kullanici-a",
		VocalTakes: []model.VocalTake{
			{FileURL: client.UnsignedURL("guides/kullanici-a/p1/ai.mp3", cfg), GainDb: &gain},
			{FileURL: client.UnsignedURL("vocals/kullanici-a/p1/s1/t1.wav", cfg)},
		},
	}

	takes, err := w.buildVocalTakes(payload)
	if err != nil {
		t.Fatalf("buildVocalTakes: %v", err)
	}
	if takes[0].GainDb == nil || *takes[0].GainDb != -6 {
		t.Errorf("take 0 gain = %v, want -6", takes[0].GainDb)
	}
	if takes[1].GainDb != nil {
		t.Errorf("take 1 gain = %v, want nil (0 dB)", *takes[1].GainDb)
	}
}
