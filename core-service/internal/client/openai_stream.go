package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// streamChatCompletions POSTs body to an OpenAI-compatible chat completions
// endpoint and feeds every choices[0].delta.content to onText, in order.
// onText's error is returned unchanged and ends the request (the response
// body is closed, which cancels the call). The deadline comes from ctx, so hc
// must have no Timeout.
func streamChatCompletions(ctx context.Context, hc *http.Client, url, apiKey string, body any, onText func(string) error) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chat API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Whole lines are read before any JSON decode, so a multi-byte rune split
	// across TCP writes is reassembled first.
	r := bufio.NewReader(resp.Body)
	for {
		line, readErr := r.ReadString('\n')
		if text, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data:"); ok {
			text = strings.TrimSpace(text)
			if text == "[DONE]" {
				return nil
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(text), &chunk); err != nil {
				return fmt.Errorf("failed to decode stream chunk: %w", err)
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				if err := onText(chunk.Choices[0].Delta.Content); err != nil {
					return err
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if ctx.Err() != nil {
				return fmt.Errorf("stream cancelled: %w", ctx.Err())
			}
			return fmt.Errorf("failed to read stream: %w", readErr)
		}
	}
}
