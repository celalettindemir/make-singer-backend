"""Encoder service for audio format conversion."""

import os
import tempfile
from typing import Any

from pydub import AudioSegment

from .storage import StorageService


class EncoderService:
    """Handles audio encoding operations."""

    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def encode(
        self,
        input_key: str,
        format: str,
        output_key: str,
        quality: int = 320,
        sample_rate: int = 48000,
        bit_depth: int = 24,
        metadata: dict[str, str] | None = None,
        **kw: Any,
    ) -> dict[str, Any]:
        """
        Encode audio to specified format.

        Supports MP3 and WAV formats with configurable quality settings.
        """
        if not self.storage:
            raise RuntimeError("Storage service not configured")

        metadata = metadata or {}

        with tempfile.TemporaryDirectory() as tmpdir:
            # Download input file (nesne anahtariyla, S3 API uzerinden)
            input_path = os.path.join(tmpdir, "input.wav")
            self.storage.download_key_to_file(input_key, input_path)

            # Load audio
            audio = AudioSegment.from_file(input_path)

            # Determine output format and settings
            if format.lower() == "mp3":
                output_path, content_type = self._encode_mp3(
                    audio, tmpdir, quality, metadata
                )
            elif format.lower() == "wav":
                output_path, content_type = self._encode_wav(
                    audio, tmpdir, sample_rate, bit_depth
                )
            else:
                raise ValueError(f"Unsupported format: {format}")

            # Get file size
            file_size = os.path.getsize(output_path)

            # Upload result
            output_url = output_path
            if self.storage:
                output_url = self.storage.upload_file(
                    output_key, output_path, content_type
                )

            return {
                "output_url": output_url,
                "format": format.lower(),
                "size": file_size,
            }

    def _encode_mp3(
        self,
        audio: AudioSegment,
        tmpdir: str,
        quality: int,
        metadata: dict[str, str],
    ) -> tuple[str, str]:
        """Encode audio to MP3 format."""
        output_path = os.path.join(tmpdir, "output.mp3")

        # Map quality to bitrate
        bitrate = f"{quality}k"

        # Add metadata if provided
        tags = {}
        if metadata:
            tags = {
                "title": metadata.get("title", ""),
                "artist": metadata.get("artist", ""),
                "album": metadata.get("album", ""),
                "year": metadata.get("year", ""),
                "genre": metadata.get("genre", ""),
            }
            # Remove empty tags
            tags = {k: v for k, v in tags.items() if v}

        audio.export(
            output_path,
            format="mp3",
            bitrate=bitrate,
            tags=tags if tags else None,
        )

        return output_path, "audio/mpeg"

    def _encode_wav(
        self,
        audio: AudioSegment,
        tmpdir: str,
        sample_rate: int,
        bit_depth: int,
    ) -> tuple[str, str]:
        """Encode audio to WAV format."""
        output_path = os.path.join(tmpdir, "output.wav")

        # Set sample width based on bit depth
        sample_width_map = {16: 2, 24: 3, 32: 4}
        sample_width = sample_width_map.get(bit_depth, 3)

        # Resample if needed
        if audio.frame_rate != sample_rate:
            audio = audio.set_frame_rate(sample_rate)

        # Set bit depth
        audio = audio.set_sample_width(sample_width)

        audio.export(
            output_path,
            format="wav",
            parameters=["-ar", str(sample_rate)],
        )

        return output_path, "audio/wav"
