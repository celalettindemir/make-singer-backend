"""Pitch shift for the guided render: the instrumental stems and the guide
move together by whole semitones, length unchanged (spec 9 §3.4)."""

import json
import os
import subprocess
import tempfile
from pathlib import Path
from typing import Any

from .storage import StorageService

MP3_BITRATE = "320k"


def pitch_ratio(semitones: int) -> float:
    return 2 ** (semitones / 12)


class PitchService:
    """Shifts R2 audio files with ffmpeg's rubberband filter."""

    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def shift(self, items: list[dict[str, Any]], semitones: int) -> list[str]:
        if not self.storage:
            raise RuntimeError("Storage service not configured")
        urls = []
        with tempfile.TemporaryDirectory() as tmpdir:
            for i, item in enumerate(items):
                ext = os.path.splitext(item["input_key"])[1] or ".mp3"
                src = os.path.join(tmpdir, f"in{i}{ext}")
                dst = os.path.join(tmpdir, f"out{i}.mp3")
                self.storage.download_key_to_file(item["input_key"], src)
                subprocess.run(  # noqa: ASYNC221 - one short ffmpeg run per file, like the encoder
                    [
                        "ffmpeg",
                        "-loglevel",
                        "error",
                        "-y",
                        "-i",
                        src,
                        "-af",
                        f"rubberband=pitch={pitch_ratio(semitones):.6f}:formant=preserved",
                        "-c:a",
                        "libmp3lame",
                        "-b:a",
                        MP3_BITRATE,
                        dst,
                    ],
                    check=True,
                    capture_output=True,
                )
                urls.append(
                    self.storage.upload_file(item["output_key"], dst, "audio/mpeg")
                )
        return urls

    async def shift_notes(self, notes_key: str, output_key: str, semitones: int) -> str:
        """Moves a guide note strip (spec 9 §4.3 JSON) by semitones."""
        if not self.storage:
            raise RuntimeError("Storage service not configured")
        with tempfile.TemporaryDirectory() as tmpdir:
            path = os.path.join(tmpdir, "notes.json")
            self.storage.download_key_to_file(notes_key, path)
            doc = json.loads(Path(path).read_text(encoding="utf-8"))
            doc["pitchShiftSemitones"] = doc.get("pitchShiftSemitones", 0) + semitones
            if doc.get("rangeMidi"):
                doc["rangeMidi"] = [m + semitones for m in doc["rangeMidi"]]
            for line in doc.get("lines", []):
                for syllable in line.get("syllables", []):
                    for note in syllable.get("notes", []):
                        note["midi"] += semitones
            Path(path).write_text(json.dumps(doc, ensure_ascii=False), encoding="utf-8")
            return self.storage.upload_file(output_key, path, "application/json")
