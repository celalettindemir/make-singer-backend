package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/pkg/response"
)

const defaultHeartbeat = 15 * time.Second

// GenerateStream handles POST /api/lyrics/generate/stream
// @Summary      Generate lyrics as a stream
// @Description  Same request as /api/lyrics/generate; answers a text/event-stream of start, line, done and error events. Errors before the stream use the JSON error envelope.
// @Tags         Lyrics
// @Accept       json
// @Produce      text/event-stream
// @Param        request body model.LyricsGenerateRequest true "Generate request"
// @Success      200 {string} string "SSE stream"
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      402 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/lyrics/generate/stream [post]
func (h *LyricsHandler) GenerateStream(c *fiber.Ctx) error {
	var req model.LyricsGenerateRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
	}
	if err := req.CheckIndex(req.SectionType); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, map[string]string{"sectionIndex": "invalid"})
	}
	act, err := h.begin(c, credit.OpLyricsGenerate)
	if act == nil {
		return err
	}
	return h.stream(c, "generate", act, func(ctx context.Context, onLine func(string) error) (int, error) {
		return h.service.GenerateStream(ctx, &req, onLine)
	})
}

// RewriteStream handles POST /api/lyrics/rewrite/stream
// @Summary      Rewrite lyrics as a stream
// @Description  Same request as /api/lyrics/rewrite; answers a text/event-stream of start, line, done and error events. Errors before the stream use the JSON error envelope.
// @Tags         Lyrics
// @Accept       json
// @Produce      text/event-stream
// @Param        request body model.LyricsRewriteRequest true "Rewrite request"
// @Success      200 {string} string "SSE stream"
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      402 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/lyrics/rewrite/stream [post]
func (h *LyricsHandler) RewriteStream(c *fiber.Ctx) error {
	var req model.LyricsRewriteRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
	}
	if err := req.CheckIndex(req.SectionType); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, map[string]string{"sectionIndex": "invalid"})
	}
	act, err := h.begin(c, credit.OpLyricsRewrite)
	if act == nil {
		return err
	}
	return h.stream(c, "rewrite", act, func(ctx context.Context, onLine func(string) error) (int, error) {
		return h.service.RewriteStream(ctx, &req, onLine)
	})
}

type streamResult struct {
	lines int
	err   error
}

// stream answers with an SSE body. Everything the writer needs is copied
// from c first: fasthttp runs the writer after the handler returned.
func (h *LyricsHandler) stream(c *fiber.Ctx, op string, act *lyricsAction, run func(ctx context.Context, onLine func(string) error) (int, error)) error {
	heartbeat := h.heartbeat
	if heartbeat <= 0 {
		heartbeat = defaultHeartbeat
	}
	errMsg := i18n.Message(response.Lang(c), i18n.KeyAIUnavailable)
	where := c.Method() + " " + c.Route().Path
	started := time.Now()

	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache")
	c.Set("X-Accel-Buffering", "no")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// The request context is not cancelled when the client leaves; a
		// failed write is the only signal, and it cancels this context.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sent, outcome := 0, "done"
		defer func() {
			log.Printf("lyrics stream %s: lines=%d dur=%s outcome=%s", op, sent, time.Since(started).Round(time.Millisecond), outcome)
		}()

		send := func(raw string) bool {
			if _, err := w.WriteString(raw); err != nil {
				return false
			}
			return w.Flush() == nil
		}
		event := func(name string, data any) bool {
			b, _ := json.Marshal(data)
			return send(fmt.Sprintf("event: %s\ndata: %s\n\n", name, b))
		}
		gone := func() { outcome = "client_gone"; cancel() }

		if !event("start", map[string]int{"balance": act.balance}) {
			gone()
			return
		}

		lines := make(chan string)
		done := make(chan streamResult, 1)
		go func() {
			n, err := run(ctx, func(l string) error {
				select {
				case lines <- l:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			done <- streamResult{n, err}
		}()

		tick := time.NewTicker(heartbeat)
		defer tick.Stop()
		for {
			select {
			case l := <-lines:
				if !event("line", map[string]string{"text": l}) {
					gone()
					return
				}
				sent++
				tick.Reset(heartbeat)
			case <-tick.C:
				if !send(": ping\n\n") {
					gone()
					return
				}
			case r := <-done:
				// Lines are handed over unbuffered, so every emitted line
				// was written before the result arrived.
				if r.err != nil || sent == 0 {
					outcome = "error"
					if r.err != nil {
						log.Printf("%s: stream failed: %v", where, r.err)
					}
					if sent == 0 {
						act.refundFor(where)
					}
					if !event("error", map[string]string{"code": response.CodeAIError, "message": errMsg}) {
						outcome = "client_gone"
					}
					return
				}
				if !event("done", map[string]int{"lines": sent}) {
					outcome = "client_gone"
				}
				return
			}
		}
	})
	return nil
}
