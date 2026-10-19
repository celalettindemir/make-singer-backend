package model

import (
	"encoding/json"
	"testing"

	"github.com/go-playground/validator/v10"
)

func intp(v int) *int { return &v }

func TestVocalTake_TimelineJSONAndValidation(t *testing.T) {
	var req MasterFinalRequest
	body := `{"vocalTakes":[{"sectionId":"s","takeId":"t","fileUrl":"https://x.test/a.wav","startMs":1000,"endMs":2000}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	take := req.VocalTakes[0]
	if take.StartMs == nil || *take.StartMs != 1000 || *take.EndMs != 2000 {
		t.Fatalf("take = %+v", take)
	}

	// The Asynq payload carries the same type; the fields must survive it.
	b, _ := json.Marshal(&MasterJobPayload{VocalTakes: req.VocalTakes})
	var back MasterJobPayload
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.VocalTakes[0]; got.StartMs == nil || *got.StartMs != 1000 || *got.EndMs != 2000 {
		t.Errorf("payload round trip lost the timeline: %s", b)
	}

	v := validator.New()
	neg := VocalTake{SectionID: "s", TakeID: "t", FileURL: "https://x.test/a.wav", StartMs: intp(-1)}
	if err := v.Struct(&neg); err == nil {
		t.Error("negative startMs accepted")
	}
	ok := VocalTake{SectionID: "s", TakeID: "t", FileURL: "https://x.test/a.wav"}
	if err := v.Struct(&ok); err != nil {
		t.Errorf("missing timeline rejected: %v", err)
	}
}

func TestMasterFinalRequest_TimelineErrors(t *testing.T) {
	cases := []struct {
		name       string
		start, end *int
		bad        bool
	}{
		{"both absent", nil, nil, false},
		{"only start", intp(500), nil, false},
		{"only end", nil, intp(500), false},
		{"end after start", intp(0), intp(1), false},
		{"end equals start", intp(500), intp(500), true},
		{"end before start", intp(500), intp(100), true},
	}
	for _, c := range cases {
		req := &MasterFinalRequest{VocalTakes: []VocalTake{{StartMs: c.start, EndMs: c.end}}}
		errs := req.TimelineErrors()
		if (errs != nil) != c.bad {
			t.Errorf("%s: errs = %v, want bad=%v", c.name, errs, c.bad)
		}
		if c.bad && errs["vocalTakes[0].endMs"] == "" {
			t.Errorf("%s: missing endMs key: %v", c.name, errs)
		}
	}
}
