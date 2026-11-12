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
                ses = AudioSegment.from_file(giris)
            except (CouldntDecodeError, IndexError) as e:
                raise HumUnreadable(str(e)) from e
            sure_ms = len(ses)
            if sure_ms <= 0:
                raise HumUnreadable("empty audio")
            ses.export(cikis, format="mp3", bitrate="192k")
            self.storage.upload_file(output_key, cikis, "audio/mpeg")
        return {"duration_ms": sure_ms}
