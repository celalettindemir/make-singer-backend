// Package push composes and sends the push notification of a finished job.
package push

import (
	"fmt"
	"strings"

	"github.com/makeasinger/api/internal/model"
)

// Title is the notification title in every language.
const Title = "MakeASinger"

// Message is one notification: visible text plus the data the app uses to open
// the project at the right step.
type Message struct {
	Title string
	Body  string
	Data  map[string]string
}

type copyText struct{ tr, en string }

var (
	lyricsDone     = copyText{"Sözlerin hazır — %s", "Your lyrics are ready — %s"}
	lyricsDoneBare = copyText{"Sözlerin hazır", "Your lyrics are ready"}
	lyricsPartial  = copyText{"Sözlerin hazır, %d bölüm yazılamadı", "Your lyrics are ready, %d sections could not be written"}
	renderDone     = copyText{"Şarkın hazır — dinlemeye gel", "Your song is ready — come listen"}
	masterDone     = copyText{"Son halin hazır", "Your final mix is ready"}
	failedRefund   = copyText{"Şarkın üretilemedi, kredin iade edildi", "Your song could not be made, your credits were refunded"}
	failedShadow   = copyText{"Şarkın üretilemedi", "Your song could not be made"}
)

func (c copyText) pick(lang string) string {
	if strings.EqualFold(lang, "tr") {
		return c.tr
	}
	// en, and fr until it has its own copy.
	return c.en
}

// Compose builds the notification of a finished job in lang (tr, en; any other
// language, fr included, gets the English copy). creditsEnforced decides
// whether a failure promises a refund.
func Compose(job model.Job, lang string, creditsEnforced bool) Message {
	var body string
	switch {
	case job.Status == model.JobStatusFailed:
		if creditsEnforced {
			body = failedRefund.pick(lang)
		} else {
			body = failedShadow.pick(lang)
		}
	case job.Type == model.JobTypeLyrics:
		failed := 0
		if r, err := model.ParseLyricsJobResult(job.Result); err == nil {
			failed = r.Count(model.LyricsSectionFailed)
		}
		switch {
		case failed > 0:
			body = fmt.Sprintf(lyricsPartial.pick(lang), failed)
		case job.ProjectName != "":
			body = fmt.Sprintf(lyricsDone.pick(lang), job.ProjectName)
		default:
			body = lyricsDoneBare.pick(lang)
		}
	case job.Type == model.JobTypeMaster:
		body = masterDone.pick(lang)
	default:
		body = renderDone.pick(lang)
	}
	return Message{
		Title: Title,
		Body:  body,
		Data: map[string]string{
			"jobId":     job.ID,
			"projectId": job.ProjectID,
			"step":      job.Type,
			"type":      job.Type,
			"status":    string(job.Status),
		},
	}
}
