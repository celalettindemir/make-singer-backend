package model

import (
	"encoding/json"
	"strings"
)

// Lyrics job modes.
const (
	LyricsModeGenerate = "generate"
	LyricsModeRewrite  = "rewrite"
)

// Status of one section inside a lyrics job result.
const (
	LyricsSectionPending = "pending"
	LyricsSectionWriting = "writing"
	LyricsSectionDone    = "done"
	LyricsSectionFailed  = "failed"
)

// LyricsJobSection is one section to write.
type LyricsJobSection struct {
	Index        int    `json:"index" validate:"min=0"`
	Instructions string `json:"instructions,omitempty" validate:"max=500"`
}

// LyricsJobRequest is the body of POST /api/lyrics/jobs.
type LyricsJobRequest struct {
	ProjectID   string              `json:"projectId" validate:"required,max=64"`
	ProjectName string              `json:"projectName,omitempty" validate:"max=120"`
	Mode        string              `json:"mode" validate:"required,oneof=generate rewrite"`
	Genre       Genre               `json:"genre" validate:"required,oneof=pop rock hiphop rnb electronic jazz country folk classical latin reggae blues"`
	Vibes       []string            `json:"vibes" validate:"required,min=1,max=3,dive,min=1"`
	Language    Language            `json:"language,omitempty" validate:"omitempty,oneof=en tr fr"`
	Topic       string              `json:"topic,omitempty" validate:"max=500"`
	BPM         *int                `json:"bpm,omitempty" validate:"omitempty,min=40,max=220"`
	Song        []LyricsSongSection `json:"song" validate:"required,min=1,max=32,dive"`
	Sections    []LyricsJobSection  `json:"sections" validate:"required,min=1,max=12,dive"`
}

// LyricsJobStartResponse is the 202 answer of POST /api/lyrics/jobs.
type LyricsJobStartResponse struct {
	JobID   string    `json:"jobId"`
	Status  JobStatus `json:"status"`
	Balance int       `json:"balance"`
}

// LyricsCharge is one credit charge of a lyrics job. Section is the song
// index it pays for, or -1 for the whole job (generate).
type LyricsCharge struct {
	Section  int    `json:"section"`
	ActionID string `json:"actionId"`
	Op       string `json:"op"`
}

// LyricsJobInput is the job's stored input (column girdi): the request plus
// server-only state. It is never sent to a client.
type LyricsJobInput struct {
	LyricsJobRequest
	// ActionID is the base action id of the start; set only when credits were
	// enforced, so Retry knows to charge.
	ActionID string         `json:"actionId,omitempty"`
	Charges  []LyricsCharge `json:"charges,omitempty"`
	// Attempt counts retries; the asynq task id is jobID:attempt.
	Attempt int `json:"attempt"`
}

// GenerateFor builds the generate request of one section; song is the song
// context to send (the request's song with lines written so far).
func (r *LyricsJobRequest) GenerateFor(sec LyricsJobSection, song []LyricsSongSection) *LyricsGenerateRequest {
	idx := sec.Index
	req := &LyricsGenerateRequest{
		Genre: r.Genre, Vibes: r.Vibes, Language: r.Language,
		LyricsContext: LyricsContext{Topic: r.Topic, BPM: r.BPM, Song: song, SectionIndex: &idx},
	}
	if idx >= 0 && idx < len(song) {
		req.SectionType = song[idx].Type
	}
	return req
}

// RewriteFor builds the rewrite request of one section. The text to rewrite
// is the request's own song[index].lyrics; song is the context to send.
func (r *LyricsJobRequest) RewriteFor(sec LyricsJobSection, song []LyricsSongSection) *LyricsRewriteRequest {
	idx := sec.Index
	req := &LyricsRewriteRequest{
		Genre: r.Genre, Vibes: r.Vibes, Language: r.Language, Instructions: sec.Instructions,
		LyricsContext: LyricsContext{Topic: r.Topic, BPM: r.BPM, Song: song, SectionIndex: &idx},
	}
	if idx >= 0 && idx < len(r.Song) {
		req.SectionType = r.Song[idx].Type
		req.CurrentLyrics = r.Song[idx].Lyrics
	}
	return req
}

// LyricsJobSectionResult is one section of a lyrics job result.
type LyricsJobSectionResult struct {
	Index  int      `json:"index"`
	Status string   `json:"status"`
	Lines  []string `json:"lines"`
}

// LyricsJobResult is the stored and served result of a lyrics job.
type LyricsJobResult struct {
	Sections []LyricsJobSectionResult `json:"sections"`
}

// NewLyricsJobResult returns the result of a job that has not started: every
// requested section pending, in writing order.
func NewLyricsJobResult(sections []LyricsJobSection) *LyricsJobResult {
	r := &LyricsJobResult{Sections: make([]LyricsJobSectionResult, 0, len(sections))}
	for _, s := range sections {
		r.Sections = append(r.Sections, LyricsJobSectionResult{Index: s.Index, Status: LyricsSectionPending, Lines: []string{}})
	}
	return r
}

// ParseLyricsJobResult decodes a stored result; an empty value is an empty
// result.
func ParseLyricsJobResult(raw []byte) (*LyricsJobResult, error) {
	r := &LyricsJobResult{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, r); err != nil {
			return nil, err
		}
	}
	if r.Sections == nil {
		r.Sections = []LyricsJobSectionResult{}
	}
	for i := range r.Sections {
		if r.Sections[i].Lines == nil {
			r.Sections[i].Lines = []string{}
		}
	}
	return r, nil
}

// Section returns the entry of song index idx, or nil.
func (r *LyricsJobResult) Section(idx int) *LyricsJobSectionResult {
	for i := range r.Sections {
		if r.Sections[i].Index == idx {
			return &r.Sections[i]
		}
	}
	return nil
}

// Count returns how many sections have the given status.
func (r *LyricsJobResult) Count(status string) int {
	n := 0
	for _, s := range r.Sections {
		if s.Status == status {
			n++
		}
	}
	return n
}

// AnyLine reports whether any section holds a written line.
func (r *LyricsJobResult) AnyLine() bool {
	for _, s := range r.Sections {
		if len(s.Lines) > 0 {
			return true
		}
	}
	return false
}

// FailedSections returns the song indexes of the failed sections; never nil.
func (r *LyricsJobResult) FailedSections() []int {
	out := []int{}
	for _, s := range r.Sections {
		if s.Status == LyricsSectionFailed {
			out = append(out, s.Index)
		}
	}
	return out
}

// Progress is the share of sections that are done or failed, 0-100.
func (r *LyricsJobResult) Progress() int {
	if len(r.Sections) == 0 {
		return 0
	}
	return (r.Count(LyricsSectionDone) + r.Count(LyricsSectionFailed)) * 100 / len(r.Sections)
}

// Text joins the lines of a section the way a song section holds them.
func (s LyricsJobSectionResult) Text() string { return strings.Join(s.Lines, "\n") }
