"""Rehber analizi testleri: sentetik ses, ag ve ffmpeg yok."""

import json

import numpy as np
import pytest
from pydantic import ValidationError
from pydub import AudioSegment

import services.guide as guide_module
from main import GuideAnalyzeRequest
from services.guide import (
    GuideService,
    build_lines,
    estimate_offset_ms,
    match_words,
    segment_notes,
    syllables,
    track_pitch,
)

RATE = 16000


def tone(midi: float, seconds: float, amp: float = 0.5) -> np.ndarray:
    hz = 440.0 * 2 ** ((midi - 69) / 12)
    t = np.arange(int(RATE * seconds)) / RATE
    return amp * np.sin(2 * np.pi * hz * t)


def silence(seconds: float) -> np.ndarray:
    return np.zeros(int(RATE * seconds))


def word(text, start, end, success=True):
    return {"word": text, "start_s": start, "end_s": end, "success": success}


def test_yin_finds_a4():
    midi = track_pitch(tone(69, 1.0), RATE, "f")
    voiced = midi[~np.isnan(midi)]
    assert len(voiced) > 50
    assert abs(np.median(voiced) - 69) < 0.3


def test_male_range_ignores_below_80hz():
    hz50 = 0.5 * np.sin(2 * np.pi * 50 * np.arange(RATE) / RATE)
    assert np.all(np.isnan(track_pitch(hz50, RATE, "m")))


def test_segment_two_notes():
    midi = np.concatenate([np.full(50, 60.0), np.full(50, 64.0)])
    notes = segment_notes(midi)
    assert [n["midi"] for n in notes] == [60, 64]
    assert abs(notes[0]["endMs"] - 500) <= 20
    assert abs(notes[1]["startMs"] - 500) <= 20


def test_short_blip_joins_neighbour():
    midi = np.concatenate([np.full(40, 60.0), np.full(5, 67.0), np.full(40, 60.0)])
    notes = segment_notes(midi)
    assert len(notes) == 1
    assert notes[0]["midi"] == 60


def test_syllables_turkish():
    assert syllables("seviyorum") == ["se", "vi", "yo", "rum"]
    assert syllables("kollarına") == ["kol", "la", "rı", "na"]
    assert syllables("güneş") == ["gü", "neş"]
    assert syllables("gel") == ["gel"]


def test_extra_chorus_and_tags_are_ignored():
    sections = [
        {"id": "v", "type": "verse", "lines": ["a b"]},
        {"id": "c", "type": "chorus", "lines": ["c d"]},
    ]
    words = [
        word("[Verse]\na ", 1.0, 1.4),
        word("b\n", 1.5, 1.9),
        word("[Chorus]\nc ", 3.0, 3.4),
        word("d\n", 3.5, 3.9),
        word("c ", 6.0, 6.4),
        word("d", 6.5, 6.9),
    ]
    times, extra = match_words(words, sections)
    assert times[(0, 0)] == (1000.0, 1400.0)
    assert times[(1, 1)] == (3500.0, 3900.0)
    assert len(times) == 4
    assert extra == 2


def test_cues_hold_rise_breath():
    sections = [{"id": "v", "type": "verse", "lines": ["la si"]}]
    notes = [
        {"startMs": 0, "endMs": 200, "midi": 60},
        {"startMs": 500, "endMs": 1300, "midi": 64},
    ]
    times = {(0, 0): (0.0, 200.0), (0, 1): (500.0, 1300.0)}
    lines = build_lines(notes, sections, times, bpm=120)
    first, second = lines[0]["syllables"]
    assert first["text"] == "la" and "breath" in first["cues"]
    assert second["text"] == "si"
    assert "hold" in second["cues"] and "rise" in second["cues"]
    assert lines[0]["startMs"] == 0 and lines[0]["endMs"] == 1300
    assert lines[0]["sectionId"] == "v"


def test_unmatched_line_has_null_times():
    sections = [{"id": "v", "type": "verse", "lines": ["la", "si"]}]
    notes = [{"startMs": 0, "endMs": 300, "midi": 60}]
    lines = build_lines(notes, sections, {(0, 0): (0.0, 300.0)}, bpm=120)
    assert len(lines) == 2
    assert lines[1]["startMs"] is None and lines[1]["endMs"] is None
    assert lines[1]["syllables"][0]["notes"] == []


def clicks(seconds: float, shift_ms: int) -> np.ndarray:
    rng = np.random.default_rng(5)
    out = np.zeros(int(RATE * seconds))
    for t in np.cumsum(rng.uniform(0.15, 0.6, 40)):
        i = int((t + shift_ms / 1000) * RATE)
        if i + 200 < len(out):
            out[i : i + 200] = rng.normal(0, 0.5, 200)
    return out


def test_offset_finds_shift():
    a = clicks(12.0, 40)
    b = clicks(12.0, 0)
    assert abs(estimate_offset_ms(a, b, RATE) - 40) <= 10


def test_silent_guide_gives_empty_notes():
    midi = track_pitch(silence(2.0), RATE, "f")
    assert np.all(np.isnan(midi))
    notes = segment_notes(midi)
    assert notes == []
    lines = build_lines(
        notes, [{"id": "v", "type": "verse", "lines": ["la"]}], {}, bpm=100
    )
    assert lines[0]["startMs"] is None


class FakeStorage:
    def __init__(self, files):
        self.files = files
        self.downloaded = []
        self.uploaded = {}

    def download_key_to_file(self, key, path):
        # The file holds the key; the patched from_file maps it back.
        self.downloaded.append(key)
        with open(path, "w") as f:
            f.write(key)

    def upload_file(self, key, path, content_type):
        with open(path, "rb") as f:
            self.uploaded[key] = (f.read(), content_type)
        return f"https://r2.test/{key}"


def segment(samples: np.ndarray) -> AudioSegment:
    data = np.round(np.clip(samples, -1, 1) * 32767).astype(np.int16)
    return AudioSegment(data.tobytes(), frame_rate=RATE, sample_width=2, channels=1)


@pytest.mark.asyncio
async def test_endpoint_writes_aligned_and_notes(monkeypatch):
    voice = np.concatenate(
        [silence(0.5), tone(64, 0.6), silence(0.4), tone(67, 0.8), silence(0.5)]
    )
    inst = clicks(3.0, 0)
    storage = FakeStorage(
        {
            "g-ham.mp3": segment(voice),
            "g-ref.mp3": segment(clicks(3.0, 40)),
            "inst.mp3": segment(inst),
        }
    )

    def fake_export(self, path, format=None, bitrate=None, **_):
        with open(path, "wb") as f:
            f.write(b"mp3")

    def fake_from_file(path, *_, **__):
        with open(path) as f:
            return storage.files[f.read()]

    monkeypatch.setattr(guide_module.AudioSegment, "export", fake_export)
    monkeypatch.setattr(guide_module.AudioSegment, "from_file", fake_from_file)
    result = await GuideService(storage).analyze(
        guide_key="g-ham.mp3",
        reference_key="g-ref.mp3",
        instrumental_key="inst.mp3",
        words=[word("[Verse]\nla ", 0.5, 1.1), word("si", 1.5, 2.3)],
        sections=[{"id": "v", "type": "verse", "lines": ["la si"]}],
        vocal_gender="f",
        bpm=100.0,
        aligned_key="g.mp3",
        notes_key="g-notes.json",
    )
    assert abs(result["offset_ms"] - 40) <= 10
    assert result["notes_url"] == "https://r2.test/g-notes.json"
    assert result["aligned_url"] == "https://r2.test/g.mp3"
    notes = json.loads(storage.uploaded["g-notes.json"][0])
    assert notes["version"] == 1
    assert notes["offsetMs"] == result["offset_ms"]
    assert len(notes["lines"]) == 1
    assert result["lines_matched"] == 1
    assert result["range_midi"] is not None


def test_request_forbids_extra():
    base = {
        "guide_key": "a",
        "reference_key": "b",
        "instrumental_key": "c",
        "words": [],
        "sections": [{"id": "v", "type": "verse", "lines": []}],
        "vocal_gender": "f",
        "bpm": 100,
        "aligned_key": "d",
        "notes_key": "e",
    }
    GuideAnalyzeRequest(**base)
    with pytest.raises(ValidationError):
        GuideAnalyzeRequest(**base, extra_field=1)
    with pytest.raises(ValidationError):
        GuideAnalyzeRequest(**{**base, "vocal_gender": "x"})
