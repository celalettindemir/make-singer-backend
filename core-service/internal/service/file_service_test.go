package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/client"
)

const signUserID = "u1"

func privateURL(key string) string {
	return "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/" + key + "?X-Amz-Signature=eski"
}

func TestFileService_SignKeepsOrderAndRefreshesURLs(t *testing.T) {
	storage := &fakeStorage{}
	svc := NewFileService(storage, testR2Cfg())
	input := []string{privateURL("stems/u1/p1/a.mp3"), privateURL("vocals/u1/p1/s1/t1.wav")}
	got, err := svc.Sign(context.Background(), signUserID, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Files) != 2 {
		t.Fatalf("expected 2 files: %+v", got.Files)
	}
	for i, key := range []string{"stems/u1/p1/a.mp3", "vocals/u1/p1/s1/t1.wav"} {
		if !strings.Contains(got.Files[i].URL, key) || strings.Contains(got.Files[i].URL, "eski") {
			t.Errorf("file %d = %q, want a fresh %s URL", i, got.Files[i].URL, key)
		}
		if got.Files[i].ExpiresAt == nil {
			t.Errorf("expiresAt expected for file %d", i)
		}
	}
}

func TestFileService_SignRejectsNotOwnedKey(t *testing.T) {
	_, err := NewFileService(&fakeStorage{}, testR2Cfg()).Sign(context.Background(), signUserID,
		[]string{privateURL("stems/u1/p1/a.mp3"), privateURL("stems/u2/p1/b.mp3")})
	if !errors.Is(err, client.ErrKeyNotOwned) {
		t.Fatalf("want ErrKeyNotOwned, got %v", err)
	}
}

func TestFileService_SignRejectsUnknownHost(t *testing.T) {
	_, err := NewFileService(&fakeStorage{}, testR2Cfg()).Sign(context.Background(), signUserID,
		[]string{"https://kotu.ornek/stems/u1/p1/a.mp3"})
	if !errors.Is(err, client.ErrInvalidURL) {
		t.Fatalf("want ErrInvalidURL, got %v", err)
	}
}

func TestFileService_SignSignsNothingWhenAnyForeign(t *testing.T) {
	storage := &fakeStorage{}
	_, _ = NewFileService(storage, testR2Cfg()).Sign(context.Background(), signUserID,
		[]string{privateURL("stems/u1/p1/a.mp3"), privateURL("stems/u2/p1/b.mp3")})
	if storage.urlForCount != 0 {
		t.Fatalf("no signature must be generated for a foreign key, URLFor was called %d times", storage.urlForCount)
	}
}

func TestFileService_SignPublicKeyHasNoExpiry(t *testing.T) {
	// storage nil: ResolveURL returns an unsigned URL and a zero time.
	got, err := NewFileService(nil, testR2Cfg()).Sign(context.Background(), signUserID,
		[]string{"https://makesinger-cdn.celalettindemir.dev/exports/u1/e1.mp3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Files[0].ExpiresAt != nil {
		t.Fatalf("a public key must not have expiresAt: %v", got.Files[0].ExpiresAt)
	}
}
