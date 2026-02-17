"""Storage service for R2/S3 operations."""

import io
from typing import BinaryIO

import boto3
import httpx


class StorageService:
    """Handles file storage operations with Cloudflare R2."""

    def __init__(
        self,
        account_id: str,
        access_key_id: str,
        secret_access_key: str,
        bucket_name: str,
        public_url: str = "",
    ):
        self.bucket_name = bucket_name
        self.public_url = public_url

        endpoint_url = f"https://{account_id}.r2.cloudflarestorage.com"

        self.s3_client = boto3.client(
            "s3",
            endpoint_url=endpoint_url,
            aws_access_key_id=access_key_id,
            aws_secret_access_key=secret_access_key,
            region_name="auto",
        )

    async def download(self, url: str) -> bytes:
        """Download a file from URL."""
        async with httpx.AsyncClient() as client:
            response = await client.get(url, timeout=120.0)
            response.raise_for_status()
            return response.content

    async def download_to_file(self, url: str, path: str) -> None:
        """Download a file from URL to local path."""
        content = await self.download(url)
        with open(path, "wb") as f:
            f.write(content)

    def upload(self, key: str, data: bytes | BinaryIO, content_type: str) -> str:
        """Upload data to R2 and return the public URL."""
        if isinstance(data, bytes):
            data = io.BytesIO(data)

        self.s3_client.upload_fileobj(
            data,
            self.bucket_name,
            key,
            ExtraArgs={"ContentType": content_type},
        )

        return self.get_public_url(key)

    def upload_file(self, key: str, path: str, content_type: str) -> str:
        """Upload a file from local path to R2."""
        with open(path, "rb") as f:
            return self.upload(key, f, content_type)

    def get_public_url(self, key: str) -> str:
        """Get the public URL for a key."""
        if self.public_url:
            return f"{self.public_url}/{key}"
        return f"https://{self.bucket_name}.r2.cloudflarestorage.com/{key}"

    def delete(self, key: str) -> None:
        """Delete a file from R2."""
        self.s3_client.delete_object(Bucket=self.bucket_name, Key=key)
