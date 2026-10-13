package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakeS3 sets up a local S3 endpoint that records CopyObject requests and
// points an R2Client at it. Nothing leaves the network.
func fakeS3(t *testing.T) (*R2Client, *[]*http.Request) {
	t.Helper()
	var (
		mu       sync.Mutex
		requests []*http.Request
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Clone(context.Background()))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><CopyObjectResult><ETag>"e"</ETag></CopyObjectResult>`))
	}))
	t.Cleanup(srv.Close)
	s3c := s3.New(s3.Options{
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider("a", "s", ""),
	})
	return &R2Client{s3Client: s3c, cfg: testR2Cfg()}, &requests
}

func TestCopy_SameBucketUsesCopyObject(t *testing.T) {
	c, requests := fakeS3(t)
	if err := c.Copy(context.Background(), "masters/u1/p1/m.wav", "sosyal/y1/master.wav"); err != nil {
		t.Fatalf("Kopyala: %v", err)
	}
	if len(*requests) != 1 {
		t.Fatalf("expected a single request: %d", len(*requests))
	}
	r := (*requests)[0]
	if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/makeasinger-private/sosyal/y1/master.wav") {
		t.Fatalf("request = %s %s", r.Method, r.URL.Path)
	}
	if got := r.Header.Get("X-Amz-Copy-Source"); got != "makeasinger-private/masters/u1/p1/m.wav" {
		t.Fatalf("kopya kaynagi = %q", got)
	}
}

func TestCopy_RejectsCrossBucket(t *testing.T) {
	c, requests := fakeS3(t)
	if err := c.Copy(context.Background(), "exports/u1/e1.mp3", "sosyal/y1/master.mp3"); err == nil {
		t.Fatal("public -> private copy should have been rejected")
	}
	if len(*requests) != 0 {
		t.Fatal("the rejected copy must not reach R2")
	}
}
