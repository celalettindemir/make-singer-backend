package jobs

import "github.com/makeasinger/api/internal/model"

// Derived is an event computed from a job state change.
type Derived struct {
	Kind string
	Data any
}

// DeriveEvents returns the events implied by a job moving from before to
// after. It is pure. Reaching succeeded, failed or canceled gives that kind
// (failed carries the error code); running is not an event; a progress or
// step change while running gives job.progress.
func DeriveEvents(before, after model.Job) []Derived {
	if after.Status != before.Status && isTerminal(after.Status) {
		switch after.Status {
		case model.JobStatusSucceeded:
			data := map[string]any{}
			if after.Type == model.JobTypeLyrics {
				// A lyrics job names the sections that could not be written.
				failed := []int{}
				if res, err := model.ParseLyricsJobResult(after.Result); err == nil {
					failed = res.FailedSections()
				}
				data["failedSections"] = failed
			}
			return []Derived{{Kind: KindSucceeded, Data: data}}
		case model.JobStatusFailed:
			return []Derived{{Kind: KindFailed, Data: map[string]any{"code": after.ErrorCode}}}
		default:
			return []Derived{{Kind: KindCanceled, Data: map[string]any{}}}
		}
	}
	if after.Status == model.JobStatusRunning &&
		(after.Progress != before.Progress || after.CurrentStep != before.CurrentStep) {
		return []Derived{{Kind: KindProgress, Data: map[string]any{
			"progress": after.Progress, "step": after.CurrentStep,
		}}}
	}
	return nil
}
