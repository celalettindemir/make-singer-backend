package client

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Copier copies an object server-side within the same bucket. It is kept
// separate from StorageClient: only social publication preparation needs it,
// and forcing every existing fake store to implement this method would be
// pointless (see PrefixDeleter).
type Copier interface {
	Copy(ctx context.Context, src, dst string) error
}

var _ Copier = (*R2Client)(nil)

// copySource is CopyObject's x-amz-copy-source value: "<bucket>/<key>".
// Keys pass the segment pattern, so no escaping is needed.
func copySource(bucket, key string) string {
	return bucket + "/" + key
}

// Copy copies the source object to the destination key. If the two keys fall
// into different buckets (e.g. exports/ -> sosyal/) it returns an error:
// cross-bucket copy needs separate permission in R2, and moving a public
// object to the private side or vice versa is a classification error.
func (c *R2Client) Copy(ctx context.Context, src, dst string) error {
	srcBucket := BucketFor(src, c.cfg)
	dstBucket := BucketFor(dst, c.cfg)
	if srcBucket != dstBucket {
		return fmt.Errorf("bucket'lar arasi kopya reddedildi: %s -> %s", srcBucket, dstBucket)
	}
	_, err := c.s3Client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(dst),
		CopySource: aws.String(copySource(srcBucket, src)),
	})
	if err != nil {
		return fmt.Errorf("R2 kopyalanamadi: %w", err)
	}
	return nil
}
