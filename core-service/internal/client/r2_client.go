package client

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/makeasinger/api/internal/config"
)

// StorageClient defines the interface for object storage operations
type StorageClient interface {
	// Upload writes the object and produces the URL to give the client in ONE
	// step (public: permanent CDN URL and zero time, private: signed URL and
	// expiry time). The caller does not need to call URLFor separately.
	Upload(ctx context.Context, key string, body io.Reader, contentType string) (string, time.Time, error)
	Delete(ctx context.Context, key string) error
	GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	// URLFor produces the right URL based on the key's prefix: a permanent CDN
	// URL for a public key (zero expiry), a presigned URL and its expiry time
	// for a private key.
	URLFor(ctx context.Context, key string) (string, time.Time, error)
}

// R2Client implements StorageClient for Cloudflare R2
type R2Client struct {
	s3Client  *s3.Client
	presigner *s3.PresignClient
	cfg       *config.R2Config
}

// NewR2Client creates a new R2 storage client
func NewR2Client(cfg *config.R2Config) (*R2Client, error) {
	if cfg.AccountID == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("R2 configuration incomplete")
	}
	if cfg.PublicBucket == "" || cfg.PrivateBucket == "" {
		return nil, fmt.Errorf("R2 bucket adlari eksik: public=%q private=%q", cfg.PublicBucket, cfg.PrivateBucket)
	}
	// R2_PUBLIC_URL is required: if empty, public keys produce neither a valid
	// CDN URL nor a correct R2 URL, and KeyFromURL does not recognize CDN URLs.
	// Failing at startup beats silently breaking in production.
	if cfg.PublicURL == "" {
		return nil, fmt.Errorf("R2_PUBLIC_URL eksik: public bucket icin CDN adresi zorunludur")
	}

	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)

	r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL: endpoint,
		}, nil
	})

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithEndpointResolverWithOptions(r2Resolver),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
		awsconfig.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg)
	presigner := s3.NewPresignClient(s3Client)

	return &R2Client{
		s3Client:  s3Client,
		presigner: presigner,
		cfg:       cfg,
	}, nil
}

// Upload uploads a file to R2 and returns its URL and expiry (public keys:
// CDN address with zero time, private keys: presigned URL with expiry).
func (c *R2Client) Upload(ctx context.Context, key string, body io.Reader, contentType string) (string, time.Time, error) {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(BucketFor(key, c.cfg)),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	}

	if _, err := c.s3Client.PutObject(ctx, input); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to upload to R2: %w", err)
	}

	return c.URLFor(ctx, key)
}

// Delete removes a file from R2
func (c *R2Client) Delete(ctx context.Context, key string) error {
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(BucketFor(key, c.cfg)),
		Key:    aws.String(key),
	}

	if _, err := c.s3Client.DeleteObject(ctx, input); err != nil {
		return fmt.Errorf("failed to delete from R2: %w", err)
	}
	return nil
}

// GetSignedURL generates a presigned URL for temporary access
func (c *R2Client) GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(BucketFor(key, c.cfg)),
		Key:    aws.String(key),
	}

	presignedReq, err := c.presigner.PresignGetObject(ctx, input, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}
	return presignedReq.URL, nil
}

// URLFor returns the address a client should use for this key.
func (c *R2Client) URLFor(ctx context.Context, key string) (string, time.Time, error) {
	if IsPublicKey(key) {
		if c.cfg.PublicURL != "" {
			return fmt.Sprintf("%s/%s", c.cfg.PublicURL, key), time.Time{}, nil
		}
		return fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s", c.cfg.PublicBucket, key), time.Time{}, nil
	}

	url, err := c.GetSignedURL(ctx, key, c.cfg.PresignTTL)
	if err != nil {
		return "", time.Time{}, err
	}
	return url, time.Now().Add(c.cfg.PresignTTL), nil
}

// IsConfigured returns true if the client has valid configuration
func (c *R2Client) IsConfigured() bool {
	return c.s3Client != nil && c.cfg != nil && c.cfg.PrivateBucket != ""
}

// PrefixDeleter deletes all objects under a prefix. It is kept separate from
// StorageClient: only account deletion needs it, and forcing every existing
// fake store to implement this method would be pointless.
type PrefixDeleter interface {
	DeletePrefix(ctx context.Context, prefix string) (int, error)
}

var _ PrefixDeleter = (*R2Client)(nil)

// DeletePrefix lists all objects under a prefix of the form "<kind>/<userId>/"
// page by page, deletes them in bulk and returns the number deleted. The
// prefix MUST have at least two segments and a trailing "/": a prefix like
// "vocals/" would delete every user's records.
func (c *R2Client) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	segments := strings.Split(prefix, "/")
	if !strings.HasSuffix(prefix, "/") || len(segments) != 3 || segments[0] == "" || segments[1] == "" {
		return 0, fmt.Errorf("guvensiz onek reddedildi: %q", prefix)
	}
	bucket := aws.String(BucketFor(prefix, c.cfg))
	paginator := s3.NewListObjectsV2Paginator(c.s3Client, &s3.ListObjectsV2Input{
		Bucket: bucket,
		Prefix: aws.String(prefix),
	})
	deleted := 0
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return deleted, fmt.Errorf("R2 listelenemedi: %w", err)
		}
		if len(page.Contents) == 0 {
			continue
		}
		// A ListObjectsV2 page returns at most 1000 objects; the DeleteObjects
		// limit is also 1000, so one call per page is enough.
		objects := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, o := range page.Contents {
			objects = append(objects, types.ObjectIdentifier{Key: o.Key})
		}
		out, err := c.s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: bucket,
			Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return deleted, fmt.Errorf("R2 silinemedi: %w", err)
		}
		if len(out.Errors) > 0 {
			return deleted, fmt.Errorf("R2 %d nesneyi silemedi", len(out.Errors))
		}
		deleted += len(objects)
	}
	return deleted, nil
}
