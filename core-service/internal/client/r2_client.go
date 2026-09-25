package client

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/makeasinger/api/internal/config"
)

// StorageClient defines the interface for object storage operations
type StorageClient interface {
	Upload(ctx context.Context, key string, body io.Reader, contentType string) (string, error)
	Delete(ctx context.Context, key string) error
	GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	// URLFor, anahtarin onegine gore dogru adresi uretir: public anahtarda
	// kalici CDN adresi (expires sifir), private anahtarda presigned URL ve
	// son kullanma ani.
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

// Upload uploads a file to R2 and returns its URL (public keys: CDN address,
// private keys: presigned URL).
func (c *R2Client) Upload(ctx context.Context, key string, body io.Reader, contentType string) (string, error) {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(BucketFor(key, c.cfg)),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	}

	if _, err := c.s3Client.PutObject(ctx, input); err != nil {
		return "", fmt.Errorf("failed to upload to R2: %w", err)
	}

	url, _, err := c.URLFor(ctx, key)
	return url, err
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
