"""Measures BPM and key of a user-uploaded source song ("bring your own song")."""

import asyncio
import json
import os
import tempfile
from pathlib import Path
from typing import Any

import numpy as np
from pydub import AudioSegment

from .analiz import mono_ornekler
from .guide import ANALYSIS_RATE, build_lines, match_words, segment_notes, track_pitch
from .key import ton_tahmini
from .storage import StorageService
from .tempo import tempo_tahmini

ANALIZ_HIZI = 22050
# A male-range pass that lands this high means the singer is female.
KADIN_MEDYAN_HZ = 250.0
GUIDE_HIZI = ANALYSIS_RATE


class ImportUnreadable(Exception):
    """File could not be decoded as audio."""


class ImportAudioService:
    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def measure(self, input_key: str) -> dict[str, Any]:
        # Blocking work (decode, analysis) must stay off the event loop.
        return await asyncio.to_thread(self._measure, input_key)

    def _measure(self, input_key: str) -> dict[str, Any]:
        ses = self._decode(input_key)
        ses = ses.set_channels(1).set_frame_rate(ANALIZ_HIZI)
        ornekler = mono_ornekler(ses)
        ton = ton_tahmini(ornekler, ANALIZ_HIZI)
        return {
            "bpm": tempo_tahmini(ornekler, ANALIZ_HIZI),
            "key": {"tonic": ton[0], "scale": ton[1]} if ton else None,
        }

    def _decode(self, input_key: str) -> AudioSegment:
        if not self.storage:
            raise RuntimeError("Storage service not configured")

        uzanti = os.path.splitext(input_key)[1] or ".mp3"
        with tempfile.TemporaryDirectory() as tmpdir:
            giris = os.path.join(tmpdir, "giris" + uzanti)
            self.storage.download_key_to_file(input_key, giris)
            try:
                return AudioSegment.from_file(giris)
            except Exception as e:  # pydub raises many types
                raise ImportUnreadable(str(e)) from e

    async def prepare(self, input_key: str) -> dict[str, Any]:
        """Decodes the whole file (no length cap) and reports its duration."""
        return await asyncio.to_thread(self._prepare, input_key)

    def _prepare(self, input_key: str) -> dict[str, Any]:
        return {"duration_ms": len(self._decode(input_key))}

    async def notes(
        self,
        vocal_key: str,
        words: list[dict[str, Any]],
        sections: list[dict[str, Any]],
        bpm: float,
        notes_key: str,
    ) -> dict[str, Any]:
        """Guide note strip from the separated vocal; same clock, offset 0."""
        return await asyncio.to_thread(
            self._notes, vocal_key, words, sections, bpm, notes_key
        )

    def _notes(
        self,
        vocal_key: str,
        words: list[dict[str, Any]],
        sections: list[dict[str, Any]],
        bpm: float,
        notes_key: str,
    ) -> dict[str, Any]:
        """Guide note strip from the separated vocal; same clock, offset 0."""
        ses = self._decode(vocal_key).set_channels(1).set_frame_rate(GUIDE_HIZI)
        ornekler = np.array(ses.get_array_of_samples(), dtype=np.float64)
        ornekler /= float(1 << (8 * ses.sample_width - 1))

        midi = track_pitch(ornekler, GUIDE_HIZI, "m")
        sesli = midi[~np.isnan(midi)]
        if (
            len(sesli)
            and 440.0 * 2 ** ((np.median(sesli) - 69) / 12) >= KADIN_MEDYAN_HZ
        ):
            midi = track_pitch(ornekler, GUIDE_HIZI, "f")
            sesli = midi[~np.isnan(midi)]

        notalar = segment_notes(midi)
        bolumler = [{"id": s["id"], "lines": s["lines"]} for s in sections]
        zamanlar, _fazla = match_words(words, bolumler)
        satirlar = build_lines(notalar, bolumler, zamanlar, bpm)
        aralik = (
            [int(np.percentile(sesli, 5)), int(np.percentile(sesli, 95))]
            if len(sesli)
            else None
        )
        belge = {
            "version": 1,
            "offsetMs": 0,
            "pitchShiftSemitones": 0,
            "rangeMidi": aralik,
            "lines": satirlar,
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            yol = os.path.join(tmpdir, "notes.json")
            Path(yol).write_text(
                json.dumps(belge, ensure_ascii=False), encoding="utf-8"
            )
            notes_url = self.storage.upload_file(notes_key, yol, "application/json")
        return {
            "notes_url": notes_url,
            "range_midi": aralik,
            "lines_matched": sum(1 for s in satirlar if s["startMs"] is not None),
        }
