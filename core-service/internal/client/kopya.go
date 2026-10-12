package client

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Kopyalayici, bir nesneyi ayni bucket icinde sunucu tarafinda kopyalar.
// StorageClient'tan ayri tutulur: yalnizca sosyal yayin hazirligi ihtiyac
// duyar ve mevcut sahte depolarin hepsini bu yontemi uygulamaya zorlamak
// gereksizdir (bkz. OnekSilici).
type Kopyalayici interface {
	Kopyala(ctx context.Context, kaynak, hedef string) error
}

var _ Kopyalayici = (*R2Client)(nil)

// kopyaKaynagi, CopyObject'in x-amz-copy-source degeridir: "<bucket>/<key>".
// Anahtarlar segment deseninden gectigi icin kacis gerekmez.
func kopyaKaynagi(bucket, key string) string {
	return bucket + "/" + key
}

// Kopyala, kaynak nesneyi hedef anahtara kopyalar. Iki anahtar farkli
// bucket'lara dusuyorsa (orn. exports/ -> sosyal/) hata doner: R2'de
// bucket'lar arasi kopya ayri yetki ister ve public bir nesnenin private
// tarafa ya da tersine tasinmasi bir siniflandirma hatasidir.
func (c *R2Client) Kopyala(ctx context.Context, kaynak, hedef string) error {
	kaynakBucket := BucketFor(kaynak, c.cfg)
	hedefBucket := BucketFor(hedef, c.cfg)
	if kaynakBucket != hedefBucket {
		return fmt.Errorf("bucket'lar arasi kopya reddedildi: %s -> %s", kaynakBucket, hedefBucket)
	}
	_, err := c.s3Client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(hedefBucket),
		Key:        aws.String(hedef),
		CopySource: aws.String(kopyaKaynagi(kaynakBucket, kaynak)),
	})
	if err != nil {
		return fmt.Errorf("R2 kopyalanamadi: %w", err)
	}
	return nil
}
