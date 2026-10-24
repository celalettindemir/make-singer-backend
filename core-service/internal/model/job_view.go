package model

import (
	"encoding/json"
	"time"
)

// JobView is the contract's Job: a background job as the apps track it.
type JobView struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Status     JobStatus  `json:"status"`
	Progress   int        `json:"progress"`
	Step       string     `json:"step,omitempty"`
	ProjectID  string     `json:"projectId"`
	LastSeq    int64      `json:"lastSeq"`
	ErrorCode  string     `json:"errorCode,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// JobDetailView is the contract's JobDetail: a Job plus its result. Result is
// the JSON null until a render or master job succeeded; a lyrics job always
// carries its lines so far.
type JobDetailView struct {
	JobView
	Result json.RawMessage `json:"result"`
}

// JobEventView is the contract's JobEvent (the owner id stays server side).
type JobEventView struct {
	JobID string          `json:"jobId"`
	Seq   int64           `json:"seq"`
	Kind  string          `json:"kind"`
	Data  json.RawMessage `json:"data"`
	At    time.Time       `json:"at"`
}

// View maps the stored job to its wire form.
func (j *Job) View() JobView {
	return JobView{
		ID: j.ID, Type: j.Type, Status: j.Status, Progress: j.Progress,
		Step: j.CurrentStep, ProjectID: j.ProjectID, LastSeq: j.LastSeq,
		ErrorCode: j.ErrorCode, CreatedAt: j.CreatedAt,
		StartedAt: j.StartedAt, FinishedAt: j.CompletedAt,
	}
}
