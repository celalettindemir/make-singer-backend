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

// sahteS3, CopyObject isteklerini kaydeden yerel bir S3 ucu kurar ve
// R2Client'i ona yonlendirir. Ag disina cikilmaz.
func sahteS3(t *testing.T) (*R2Client, *[]*http.Request) {
	t.Helper()
	var (
		mu       sync.Mutex
		istekler []*http.Request
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		istekler = append(istekler, r.Clone(context.Background()))
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
	return &R2Client{s3Client: s3c, cfg: testR2Cfg()}, &istekler
}

func TestKopyala_AyniBucketIcindeCopyObject(t *testing.T) {
	c, istekler := sahteS3(t)
	if err := c.Kopyala(context.Background(), "masters/u1/p1/m.wav", "sosyal/y1/master.wav"); err != nil {
		t.Fatalf("Kopyala: %v", err)
	}
	if len(*istekler) != 1 {
		t.Fatalf("tek istek bekleniyordu: %d", len(*istekler))
	}
	r := (*istekler)[0]
	if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/makeasinger-private/sosyal/y1/master.wav") {
		t.Fatalf("istek = %s %s", r.Method, r.URL.Path)
	}
	if got := r.Header.Get("X-Amz-Copy-Source"); got != "makeasinger-private/masters/u1/p1/m.wav" {
		t.Fatalf("kopya kaynagi = %q", got)
	}
}

func TestKopyala_BucketlarArasiReddedilir(t *testing.T) {
	c, istekler := sahteS3(t)
	if err := c.Kopyala(context.Background(), "exports/u1/e1.mp3", "sosyal/y1/master.mp3"); err == nil {
		t.Fatal("public -> private kopya reddedilmeliydi")
	}
	if len(*istekler) != 0 {
		t.Fatal("reddedilen kopya R2'ye gitmemeli")
	}
}
