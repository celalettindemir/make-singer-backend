"""Mirilti hazirlama: yuklenen kaydi cozer, suresini olcer, mp3 olarak saklar.

Govde engelleyici is yaptigi icin asyncio.to_thread ile calisir; olay
dongusunde calisirsa liveness yaniti kesilir.
"""

import asyncio
import os
import tempfile
from typing import Any

from pydub import AudioSegment
from pydub.exceptions import CouldntDecodeError

from .storage import StorageService

# The server accepts 5-30 s (+0.5 s). ffmpeg stops after this many seconds so a
# small but highly compressible upload (hours of 6 kbps Opus) cannot be decoded
# whole into memory; a longer file then reads as 31 s and is rejected by the
# server's range check.
DECODE_LIMIT_S = 31


class HumUnreadable(Exception):
    """Dosya ses olarak cozulemedi ya da bos."""


class HumService:
    def __init__(self, storage: StorageService | None):
        self.storage = storage

    async def prepare(self, input_key: str, output_key: str) -> dict[str, Any]:
        if not self.storage:
            raise RuntimeError("Storage service not configured")
        return await asyncio.to_thread(self._prepare, input_key, output_key)

    def _prepare(self, input_key: str, output_key: str) -> dict[str, Any]:
        uzanti = os.path.splitext(input_key)[1] or ".wav"
        with tempfile.TemporaryDirectory() as tmpdir:
            giris = os.path.join(tmpdir, "giris" + uzanti)
            cikis = os.path.join(tmpdir, "cikis.mp3")
            self.storage.download_key_to_file(input_key, giris)
            try:
                ses = AudioSegment.from_file(giris, duration=DECODE_LIMIT_S)
            except (CouldntDecodeError, IndexError) as e:
                raise HumUnreadable(str(e)) from e
            sure_ms = len(ses)
            if sure_ms <= 0:
                raise HumUnreadable("empty audio")
            ses.export(cikis, format="mp3", bitrate="192k")
            self.storage.upload_file(output_key, cikis, "audio/mpeg")
        return {"duration_ms": sure_ms}
