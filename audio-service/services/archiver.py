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

        Each file entry should have 'url' and 'filename' keys.
        """
        if not files:
            raise ValueError("No files provided")

        with tempfile.TemporaryDirectory() as tmpdir:
            zip_path = os.path.join(tmpdir, "archive.zip")

            with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as zipf:
                for file_entry in files:
                    url = file_entry.get("url")
                    filename = file_entry.get("filename")

                    if not url or not filename:
                        continue

                    # Download file
                    temp_path = os.path.join(tmpdir, os.path.basename(filename))
                    if self.storage:
                        await self.storage.download_to_file(url, temp_path)

                        # Add to ZIP with specified filename
                        zipf.write(temp_path, filename)

            # Get file size
            file_size = os.path.getsize(zip_path)

            # Upload result
            output_url = zip_path
            if self.storage:
                output_url = self.storage.upload_file(
                    output_key, zip_path, "application/zip"
                )

            return {
                "output_url": output_url,
                "size": file_size,
                "file_count": len(files),
            }
