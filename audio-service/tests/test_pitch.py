"""Perde kaydirma testleri: ffmpeg cagrisi taklit edilir."""

import subprocess

import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError

import main
import services.pitch as pitch_module
from main import PitchShiftRequest
from services.pitch import PitchService, pitch_ratio


class FakeStorage:
    def __init__(self):
        self.downloaded = []
        self.uploaded = []

    def download_key_to_file(self, key, path):
        self.downloaded.append(key)
        with open(path, "wb") as f:
            f.write(b"x")

    def upload_file(self, key, path, content_type):
        self.uploaded.append((key, content_type))
        return f"https://r2.test/{key}"


def test_pitch_ratio():
    assert pitch_ratio(-3) == 2 ** (-3 / 12)
    assert pitch_ratio(4) == 2 ** (4 / 12)


@pytest.mark.asyncio
async def test_command_uses_rubberband(monkeypatch):
    calls = []

    def fake_run(args, **kwargs):
        calls.append(args)
        with open(args[-1], "wb") as f:
            f.write(b"y")
        return subprocess.CompletedProcess(args, 0)

    monkeypatch.setattr(pitch_module.subprocess, "run", fake_run)
    storage = FakeStorage()
    urls = await PitchService(storage).shift(
        [{"input_key": "stems/u/p/a.mp3", "output_key": "stems/u/p/a-km3.mp3"}], -3
    )
    assert urls == ["https://r2.test/stems/u/p/a-km3.mp3"]
    assert storage.uploaded == [("stems/u/p/a-km3.mp3", "audio/mpeg")]
    joined = " ".join(calls[0])
    assert "rubberband=pitch=" in joined and "formant=preserved" in joined
    assert "libmp3lame" in joined


def test_semitones_bounds():
    item = [{"input_key": "a", "output_key": "b"}]
    PitchShiftRequest(items=item, semitones=4)
    for bad in (0, 5, -5):
        with pytest.raises(ValidationError):
            PitchShiftRequest(items=item, semitones=bad)


def test_ffmpeg_failure_is_500(monkeypatch):
    def failing_run(args, **kwargs):
        raise subprocess.CalledProcessError(1, args, stderr=b"no filter")

    monkeypatch.setattr(pitch_module.subprocess, "run", failing_run)
    monkeypatch.setattr(main, "pitch_service", PitchService(FakeStorage()))
    client = TestClient(main.app)
    resp = client.post(
        "/perde-kaydir",
        json={"items": [{"input_key": "a.mp3", "output_key": "b.mp3"}], "semitones": 2},
    )
    assert resp.status_code == 500


@pytest.mark.asyncio
async def test_notes_are_shifted():
    import json

    doc = {
        "version": 1,
        "offsetMs": 40,
        "pitchShiftSemitones": 0,
        "rangeMidi": [60, 67],
        "lines": [
            {
                "sectionId": "v",
                "startMs": 0,
                "endMs": 500,
                "syllables": [
                    {
                        "text": "la",
                        "notes": [{"startMs": 0, "endMs": 500, "midi": 64}],
                        "cues": [],
                    }
                ],
            }
        ],
    }

    class NotesStorage(FakeStorage):
        def download_key_to_file(self, key, path):
            self.downloaded.append(key)
            with open(path, "w") as f:
                json.dump(doc, f)

        def upload_file(self, key, path, content_type):
            with open(path) as f:
                self.written = json.load(f)
            return super().upload_file(key, path, content_type)

    storage = NotesStorage()
    url = await PitchService(storage).shift_notes(
        "g-notes.json", "g-notes-km4.json", -4
    )
    assert url == "https://r2.test/g-notes-km4.json"
    assert storage.uploaded == [("g-notes-km4.json", "application/json")]
    assert storage.written["pitchShiftSemitones"] == -4
    assert storage.written["rangeMidi"] == [56, 63]
    assert storage.written["lines"][0]["syllables"][0]["notes"][0]["midi"] == 60


def test_request_accepts_notes():
    item = [{"input_key": "a", "output_key": "b"}]
    req = PitchShiftRequest(
        items=item, semitones=2, notes_key="n.json", notes_output_key="n-k2.json"
    )
    assert req.notes_key == "n.json"
