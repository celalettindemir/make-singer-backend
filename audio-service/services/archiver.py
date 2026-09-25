"""Archiver service for creating ZIP files."""

import os
import tempfile
import zipfile
from typing import Any

from .storage import StorageService


class ArchiverService:
    """Handles ZIP archive creation."""

    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def process(
        self,
        files: list[dict[str, str]],
        output_key: str,
    ) -> dict[str, Any]:
        """
        Create a ZIP archive from multiple files.

        Each file entry should have 'key' and 'filename' keys.
        """
        if not files:
            raise ValueError("No files provided")

        # encoder/master ile ayni kural: depolama yoksa sessizce bos/eksik
        # ZIP uretip "basarili" demek yerine hemen hata ver.
        if not self.storage:
            raise RuntimeError("Storage service not configured")

        with tempfile.TemporaryDirectory() as tmpdir:
            zip_path = os.path.join(tmpdir, "archive.zip")

            with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as zipf:
                for file_entry in files:
                    key = file_entry.get("key")
                    filename = file_entry.get("filename")

                    # Eksik girdiyi atlamak, file_count dogru ama icerigi
                    # eksik bir ZIP uretirdi: sessiz gecmek yerine hata ver.
                    if not key or not filename:
                        raise ValueError(
                            f"ZIP girdisinde key veya filename eksik: {file_entry!r}"
                        )

                    # Download file (nesne anahtariyla, S3 API uzerinden)
                    temp_path = os.path.join(tmpdir, os.path.basename(filename))
                    self.storage.download_key_to_file(key, temp_path)

                    # Add to ZIP with specified filename
                    zipf.write(temp_path, filename)

            # Get file size
            file_size = os.path.getsize(zip_path)

            # Upload result
            output_url = self.storage.upload_file(
                output_key, zip_path, "application/zip"
            )

            return {
                "output_url": output_url,
                "size": file_size,
                "file_count": len(files),
            }
