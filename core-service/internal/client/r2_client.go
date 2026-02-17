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
	GetPublicURL(key string) string
}

// R2Client implements StorageClient for Cloudflare R2
type R2Client struct {
	s3Client   *s3.Client
	presigner  *s3.PresignClient
	bucketName string
	publicURL  string
}

// NewR2Client creates a new R2 storage client
func NewR2Client(cfg *config.R2Config) (*R2Client, error) {
	if cfg.AccountID == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("R2 configuration incomplete")
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
		s3Client:   s3Client,
		presigner:  presigner,
		bucketName: cfg.BucketName,
		publicURL:  cfg.PublicURL,
	}, nil
}

// Upload uploads a file to R2 and returns the public URL
func (c *R2Client) Upload(ctx context.Context, key string, body io.Reader, contentType string) (string, error) {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(c.bucketName),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	}

	_, err := c.s3Client.PutObject(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to upload to R2: %w", err)
	}

	return c.GetPublicURL(key), nil
}

// Delete removes a file from R2
func (c *R2Client) Delete(ctx context.Context, key string) error {
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(key),
	}

	_, err := c.s3Client.DeleteObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete from R2: %w", err)
	}

	return nil
}

// GetSignedURL generates a presigned URL for temporary access
func (c *R2Client) GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(key),
	}

	presignedReq, err := c.presigner.PresignGetObject(ctx, input, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return presignedReq.URL, nil
}

// GetPublicURL returns the public CDN URL for a key
func (c *R2Client) GetPublicURL(key string) string {
	if c.publicURL != "" {
		return fmt.Sprintf("%s/%s", c.publicURL, key)
	}
	return fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s", c.bucketName, key)
}

// IsConfigured returns true if the client has valid configuration
func (c *R2Client) IsConfigured() bool {
	return c.s3Client != nil && c.bucketName != ""
}
