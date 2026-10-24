package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/redis/go-redis/v9"
)

const sunoWebhookMaxBody = 1 << 20

// SunoWebhookHandler receives Suno's task callback. It is wake-only: the body
// is never trusted, it only names the task whose waiting poll should run now.
type SunoWebhookHandler struct {
	rdb   *redis.Client
	token string
}

func NewSunoWebhookHandler(rdb *redis.Client, token string) *SunoWebhookHandler {
	return &SunoWebhookHandler{rdb: rdb, token: token}
}

// Handle serves POST /api/webhooks/suno/:token. A wrong token is 404; any
// other call answers 200 {} so Suno does not retry.
func (h *SunoWebhookHandler) Handle(c *fiber.Ctx) error {
	if h.token == "" || subtle.ConstantTimeCompare([]byte(c.Params("token")), []byte(h.token)) != 1 {
		return c.SendStatus(fiber.StatusNotFound)
	}
	body := c.Body()
	if len(body) <= sunoWebhookMaxBody {
		var req struct {
			Data struct {
				TaskIDSnake string `json:"task_id"`
				TaskIDCamel string `json:"taskId"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &req) == nil {
			id := req.Data.TaskIDSnake
			if id == "" {
				id = req.Data.TaskIDCamel
			}
			if id != "" && len(id) <= 128 {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if err := h.rdb.Publish(ctx, client.SunoWakeChannel(id), "1").Err(); err != nil {
					log.Printf("[Suno webhook] publish failed: %v", err)
				}
			}
		}
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{})
}
