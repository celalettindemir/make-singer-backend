"""Storage service for R2/S3 operations."""

import io
from typing import BinaryIO

import boto3

# Yalnizca bu onekteki nesneler public bucket'a gider. Ayni kural Go'da
# internal/client/bucket.go icindeki publicOnek sabiti; ikisi birlikte
# degistirilir.
PUBLIC_ONEK = "exports/"


def bucket_for(key: str, public_bucket: str, private_bucket: str) -> str:
    """Anahtarin yazilacagi/okunacagi bucket adi.

    Bilinmeyen onek PRIVATE'a duser: yanlis tarafa dusen bir dosya sizinti
    degil, yalnizca erisilemezlik uretsin.
    """
    if key.startswith(PUBLIC_ONEK):
        return public_bucket
    return private_bucket


class StorageService:
    """Handles file storage operations with Cloudflare R2."""

    def __init__(
        self,
        account_id: str,
        access_key_id: str,
        secret_access_key: str,
        public_bucket: str,
        private_bucket: str,
        public_url: str = "",
        s3_client=None,
    ):
        self.public_bucket = public_bucket
        self.private_bucket = private_bucket
        self.public_url = public_url

        if s3_client is not None:
            # Testler sahte istemci enjekte eder; ag erisimi olmaz.
            self.s3_client = s3_client
            return

        endpoint_url = f"https://{account_id}.r2.cloudflarestorage.com"

        self.s3_client = boto3.client(
            "s3",
            endpoint_url=endpoint_url,
            aws_access_key_id=access_key_id,
            aws_secret_access_key=secret_access_key,
            region_name="auto",
        )

    def _bucket(self, key: str) -> str:
        return bucket_for(key, self.public_bucket, self.private_bucket)

    def download_key_to_file(self, key: str, path: str) -> None:
        """Nesneyi S3 API ile indirir.

        Servisler arasi aktarim presigned URL KULLANMAZ: private nesneler
        icin acik bir adres uretmek gereksiz risk, ayrica yavas.
        """
        self.s3_client.download_file(self._bucket(key), key, path)

    def upload(self, key: str, data: bytes | BinaryIO, content_type: str) -> str:
        """Upload data to R2 and return its URL."""
        if isinstance(data, bytes):
            data = io.BytesIO(data)

        self.s3_client.upload_fileobj(
            data,
            self._bucket(key),
            key,
            ExtraArgs={"ContentType": content_type},
        )

        return self.url_for(key)

    def upload_file(self, key: str, path: str, content_type: str) -> str:
        """Upload a file from local path to R2."""
        with open(path, "rb") as f:
            return self.upload(key, f, content_type)

    def url_for(self, key: str) -> str:
        """Anahtarin adresi. Public anahtarda CDN, private anahtarda R2 ucu.

        Private nesneler icin istemciye giden presigned URL'yi core-service
        uretir (URLFor); burada uretilen adres yalnizca Go'ya donen
        output_url alanidir ve Go onu anahtara cevirip yeniden imzalar.
        """
        bucket = self._bucket(key)
        if bucket == self.public_bucket and self.public_url:
            return f"{self.public_url}/{key}"
        return f"https://{bucket}.r2.cloudflarestorage.com/{key}"

    def delete(self, key: str) -> None:
        """Delete a file from R2."""
        self.s3_client.delete_object(Bucket=self._bucket(key), Key=key)
