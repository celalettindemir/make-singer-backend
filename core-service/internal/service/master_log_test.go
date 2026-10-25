package service

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/model"
)

func TestLogMasterTakes_JobIDAndNoURL(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	start, end := 1000, 9000
	logMasterTakes("u1", "job-9", &model.MasterFinalRequest{
		ProjectID: "p1",
		VocalTakes: []model.VocalTake{
			{SectionID: "s1", TakeID: "t1", FileURL: "https://x/y?X-Amz-Signature=secret", StartMs: &start, EndMs: &end},
			{SectionID: "s2", TakeID: "t2", FileURL: "https://x/z"},
		},
	})
	out := buf.String()
	for _, want := range []string{"job=job-9", "take=t1", "startMs=1000", "endMs=9000", "take=t2", "startMs=-"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Signature") || strings.Contains(out, "https://") {
		t.Errorf("file URL leaked:\n%s", out)
	}
}
