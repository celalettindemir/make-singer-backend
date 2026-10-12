package client

import (
	"context"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func TestDeletePrefix_RejectsUnsafePrefix(t *testing.T) {
	c, err := NewR2Client(&config.R2Config{
		AccountID: "hesap", AccessKeyID: "a", SecretAccessKey: "s",
		PublicBucket: "pub", PrivateBucket: "priv", PublicURL: "https://cdn.ornek",
	})
	if err != nil {
		t.Fatalf("istemci kurulamadi: %v", err)
	}
	for _, prefix := range []string{"", "vocals/", "vocals", "vocals/u1", "/u1/"} {
		if _, err := c.DeletePrefix(context.Background(), prefix); err == nil {
			t.Errorf("%q kabul edilmemeliydi", prefix)
		}
	}
}
