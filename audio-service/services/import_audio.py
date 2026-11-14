"""Measures BPM and key of a user-uploaded source song ("bring your own song")."""

import os
import tempfile
from typing import Any

from pydub import AudioSegment

from .analiz import mono_ornekler
from .key import ton_tahmini
from .storage import StorageService
from .tempo import tempo_tahmini

ANALIZ_HIZI = 22050


class ImportUnreadable(Exception):
    """File could not be decoded as audio."""


class ImportAudioService:
    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def measure(self, input_key: str) -> dict[str, Any]:
        if not self.storage:
            raise RuntimeError("Storage service not configured")

        uzanti = os.path.splitext(input_key)[1] or ".mp3"
        with tempfile.TemporaryDirectory() as tmpdir:
            giris = os.path.join(tmpdir, "giris" + uzanti)
            self.storage.download_key_to_file(input_key, giris)
            try:
                ses = AudioSegment.from_file(giris)
            except Exception as e:  # pydub raises many types
                raise ImportUnreadable(str(e)) from e

        ses = ses.set_channels(1).set_frame_rate(ANALIZ_HIZI)
        ornekler = mono_ornekler(ses)
        ton = ton_tahmini(ornekler, ANALIZ_HIZI)
        return {
            "bpm": tempo_tahmini(ornekler, ANALIZ_HIZI),
            "key": {"tonic": ton[0], "scale": ton[1]} if ton else None,
        }
