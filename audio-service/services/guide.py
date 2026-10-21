"""Guide singer analysis: align the guide to the instrumental and turn it into
a note strip (spec 9 §4.3, Faz 0 `_spikes/guide-9/analyze.py`).

Pure numpy so it runs (and is tested) without librosa or ffmpeg.
"""

import difflib
import itertools
import json
import os
import re
import string
import tempfile
from pathlib import Path
from typing import Any

import numpy as np
from pydub import AudioSegment

from .storage import StorageService

# Analysis rate: enough for singing, cheap for YIN.
ANALYSIS_RATE = 16000
FRAME = 2048
# Loud frames analysed per FFT batch (memory bound).
PITCH_CHUNK = 512
YIN_THRESHOLD = 0.15
# Guide singer pitch range per voice (Faz 0 §6.4: a female guide tracked
# down to 65 Hz picked up leakage as fake low notes).
PITCH_RANGE_HZ = {"f": (175.0, 1000.0), "m": (80.0, 520.0)}
# Frames quieter than the loudest frame by more than this are not sung.
RMS_FLOOR_DB = 35.0
# A ±12 semitone jump this short is a tracker octave error.
OCTAVE_BLIP_MS = 60
# Segmentation.
SMOOTH_FRAMES = 5
NOTE_CHANGE_SEMITONES = 0.7
NOTE_GAP_MS = 80
MIN_NOTE_MS = 80
# Cues.
HOLD_BEATS = 2
HOLD_MS = 600
RISE_SEMITONES = 3
BREATH_MS = 250
# Offset search window.
MAX_OFFSET_MS = 2000

VOWELS = set("aeıioöuüâîû")
_PUNCT = string.punctuation + "“”‘’…«»"
# A word keeps the notes sung this long after Suno's word end.
WORD_TAIL_MS = 150
_TAG = re.compile(r"\[[^\]]*\]")


# ------------------------------------------------------------------ pitch


def _frames(x: np.ndarray, frame: int, hop: int) -> np.ndarray:
    if len(x) < frame:
        x = np.pad(x, (0, frame - len(x)))
    count = 1 + (len(x) - frame) // hop
    idx = np.arange(frame)[None, :] + hop * np.arange(count)[:, None]
    return x[idx]


def track_pitch(samples: np.ndarray, rate: int, gender: str) -> np.ndarray:
    """MIDI pitch every 10 ms (YIN); NaN where nothing is sung.

    Frames are a strided view and only loud frames are analysed, in chunks
    of PITCH_CHUNK, so a whole song stays far below the pod's memory limit.
    """
    fmin, fmax = PITCH_RANGE_HZ[gender]
    hop = rate // 100
    tau_min = max(2, int(rate / fmax))
    tau_max = min(FRAME // 2, int(rate / fmin) + 1)
    x = np.asarray(samples, dtype=np.float64)
    if len(x) < FRAME:
        x = np.pad(x, (0, FRAME - len(x)))
    frames = np.lib.stride_tricks.sliding_window_view(x, FRAME)[::hop]

    midi = np.full(len(frames), np.nan)
    # Frame energy from a running sum: no per-frame copy.
    csum = np.concatenate([[0.0], np.cumsum(x * x)])
    starts = np.arange(len(frames)) * hop
    rms = np.sqrt((csum[starts + FRAME] - csum[starts]) / FRAME)
    peak = rms.max() if len(rms) else 0.0
    if peak <= 1e-6:
        return midi
    loud = np.flatnonzero(20 * np.log10(np.maximum(rms, 1e-12) / peak) > -RMS_FLOOR_DB)

    width = FRAME - tau_max
    size = 1 << int(np.ceil(np.log2(FRAME + width)))
    taus = np.arange(tau_max + 1)
    for c0 in range(0, len(loud), PITCH_CHUNK):
        idx = loud[c0 : c0 + PITCH_CHUNK]
        chunk = frames[idx]
        # Difference function d(tau) = e0 + e_tau - 2 r(tau), r by FFT.
        spec_full = np.fft.rfft(chunk, n=size, axis=1)
        spec_head = np.fft.rfft(chunk[:, :width], n=size, axis=1)
        r = np.fft.irfft(spec_full * np.conj(spec_head), n=size, axis=1)[
            :, : tau_max + 1
        ]
        del spec_full, spec_head
        sq = np.cumsum(np.pad(chunk**2, ((0, 0), (1, 0))), axis=1)
        e0 = sq[:, width][:, None]
        e_tau = sq[:, taus + width] - sq[:, taus]
        d = np.maximum(e0 + e_tau - 2 * r, 0.0)
        # Cumulative mean normalized difference.
        cmnd = np.ones_like(d)
        cmnd[:, 1:] = (
            d[:, 1:] * taus[1:] / np.maximum(np.cumsum(d[:, 1:], axis=1), 1e-12)
        )
        for row, i in zip(cmnd, idx, strict=True):
            hz = _yin_pick(row, tau_min, tau_max, rate)
            if hz is not None and fmin <= hz <= fmax:
                midi[i] = 69 + 12 * np.log2(hz / 440.0)
    return _fix_octaves(midi)


def _yin_pick(row: np.ndarray, tau_min: int, tau_max: int, rate: int) -> float | None:
    """The first dip under the threshold, refined by parabolic interpolation."""
    below = np.flatnonzero(row[tau_min : tau_max + 1] < YIN_THRESHOLD)
    if not len(below):
        return None
    t = tau_min + below[0]
    while t + 1 <= tau_max and row[t + 1] < row[t]:
        t += 1
    if t >= tau_max:
        return None  # the minimum is at the edge: below fmin
    a, b, c = row[t - 1], row[t], row[t + 1]
    denom = a - 2 * b + c
    shift = 0.5 * (a - c) / denom if abs(denom) > 1e-12 else 0.0
    return rate / (t + shift)


def _fix_octaves(midi: np.ndarray) -> np.ndarray:
    out = midi.copy()
    max_len = OCTAVE_BLIP_MS // 10
    i = 1
    while i < len(out):
        prev = out[i - 1]
        if np.isnan(prev) or np.isnan(out[i]) or abs(abs(out[i] - prev) - 12) >= 1.5:
            i += 1
            continue
        jump = out[i] - prev
        j = i
        while j < len(out) and not np.isnan(out[j]) and abs(out[j] - prev - jump) < 1.5:
            j += 1
        if j - i <= max_len:
            out[i:j] -= jump
        i = j
    return out


def segment_notes(midi: np.ndarray) -> list[dict[str, int]]:
    """Notes {startMs, endMs, midi} from a 10 ms MIDI track."""
    n = len(midi)
    smooth = np.full(n, np.nan)
    half = SMOOTH_FRAMES // 2
    for i in range(n):
        if np.isnan(midi[i]):
            continue
        win = midi[max(0, i - half) : i + half + 1]
        smooth[i] = np.median(win[~np.isnan(win)])

    gap = NOTE_GAP_MS // 10
    spans, start, silent = [], None, 0
    for i, m in enumerate(smooth):
        if np.isnan(m):
            silent += 1
            if start is not None and silent > gap:
                spans.append((start, i - silent + 1))
                start = None
            continue
        if start is None:
            start = i
        elif (
            silent == 0
            and abs(m - np.median(smooth[start:i][~np.isnan(smooth[start:i])]))
            > NOTE_CHANGE_SEMITONES
        ):
            spans.append((start, i))
            start = i
        silent = 0
    if start is not None:
        end = n
        while end > start and np.isnan(smooth[end - 1]):
            end -= 1
        spans.append((start, end))

    notes: list[dict[str, int]] = []
    for a, b in spans:
        vals = smooth[a:b][~np.isnan(smooth[a:b])]
        if not len(vals):
            continue
        note = {
            "startMs": a * 10,
            "endMs": b * 10,
            "midi": round(float(np.median(vals))),
        }
        if note["endMs"] - note["startMs"] < MIN_NOTE_MS:
            if notes and note["startMs"] - notes[-1]["endMs"] <= NOTE_GAP_MS:
                notes[-1]["endMs"] = note["endMs"]  # too short: joins its neighbour
            continue
        if (
            notes
            and notes[-1]["midi"] == note["midi"]
            and note["startMs"] - notes[-1]["endMs"] <= NOTE_GAP_MS
        ):
            notes[-1]["endMs"] = note["endMs"]
            continue
        notes.append(note)
    return notes


# ------------------------------------------------------------------ lyrics


def _norm(word: str) -> str:
    w = word.replace("İ", "i").replace("I", "ı").lower()
    w = re.sub(r"[^\wçğıöşüâîû]", "", w)
    return re.sub(r"(.)\1+", r"\1", w)  # "sensiiiz" -> "sensiz"


def _lower_tr(ch: str) -> str:
    """One character lowercased the Turkish way, always one character."""
    return {"İ": "i", "I": "ı"}.get(ch, ch.lower()[:1])


def syllables(word: str, language: str = "tr") -> list[str]:
    """Turkish syllables: each vowel is a nucleus, a single consonant between
    two vowels and the last of a consonant cluster start the next syllable."""
    if language != "tr":
        return [word]
    idx = [i for i, ch in enumerate(word) if _lower_tr(ch) in VOWELS]
    if len(idx) <= 1:
        return [word]
    cuts = [b if b - a == 1 else b - 1 for a, b in itertools.pairwise(idx)]
    parts, prev = [], 0
    for c in cuts:
        parts.append(word[prev:c])
        prev = c
    parts.append(word[prev:])
    return [p for p in parts if p]


def _lyric_words(sections: list[dict[str, Any]]) -> list[list[list[str]]]:
    """Per line, its words; lines are numbered across sections."""
    return [line.split() for s in sections for line in s["lines"]]


def match_words(
    words: list[dict[str, Any]], sections: list[dict[str, Any]]
) -> tuple[dict[tuple[int, int], tuple[float, float]], int]:
    """Suno's words matched in order to (line, word) of the lyrics, in ms.

    Section tags are dropped. The two token lists are aligned as a whole
    (difflib), so a skipped or changed line only loses its own words; Suno
    words left over (an extra chorus, a changed word) are counted as extra.
    """
    lines = _lyric_words(sections)
    flat = [(li, wi) for li, ws in enumerate(lines) for wi, _ in enumerate(ws)]
    lyric_tokens = [_norm(w) for ws in lines for w in ws]
    tokens, sources = [], []
    for w in words:
        for tok in _TAG.sub(" ", w["word"]).split():
            if _norm(tok):
                tokens.append(_norm(tok))
                sources.append(w)
    matcher = difflib.SequenceMatcher(None, tokens, lyric_tokens, autojunk=False)
    times: dict[tuple[int, int], tuple[float, float]] = {}
    matched = 0
    for block in matcher.get_matching_blocks():
        for k in range(block.size):
            matched += 1
            w = sources[block.a + k]
            if w.get("success", True):
                times[flat[block.b + k]] = (w["start_s"] * 1000.0, w["end_s"] * 1000.0)
    return times, len(tokens) - matched


def _display(word: str) -> str:
    """The word as the user wrote it, without surrounding punctuation."""
    return word.strip(_PUNCT) or word


def _note_owners(
    notes: list[dict[str, int]], times: dict[tuple[int, int], tuple[float, float]]
) -> dict[tuple[int, int], list[dict[str, int]]]:
    """Each note goes to the one word it overlaps most (sung tails count)."""
    owners: dict[tuple[int, int], list[dict[str, int]]] = {}
    spans = sorted(times.items(), key=lambda kv: kv[1][0])
    for n in notes:
        best, best_overlap = None, 0.0
        for key, (start, end) in spans:
            overlap = min(n["endMs"], end + WORD_TAIL_MS) - max(n["startMs"], start)
            if overlap > best_overlap:
                best, best_overlap = key, overlap
        if best is not None:
            owners.setdefault(best, []).append(n)
    return owners


def build_lines(
    notes: list[dict[str, int]],
    sections: list[dict[str, Any]],
    times: dict[tuple[int, int], tuple[float, float]],
    bpm: float,
) -> list[dict[str, Any]]:
    """Lines in lyric order with their syllables, notes and cues."""
    beat_ms = 60000.0 / bpm if bpm and bpm > 0 else 500.0
    owners = _note_owners(notes, times)
    out, prev_midi, li = [], None, 0
    for section in sections:
        for line in section["lines"]:
            sylls: list[dict[str, Any]] = []
            for wi, word in enumerate(line.split()):
                parts = syllables(_display(word))
                in_word = owners.get((li, wi), [])
                per: list[list[dict[str, int]]] = [[] for _ in parts]
                for i, n in enumerate(in_word):
                    per[
                        min(i * len(parts) // max(len(in_word), 1), len(parts) - 1)
                    ].append(n)
                sylls += [
                    {"text": p, "notes": ns, "cues": []} for p, ns in zip(parts, per)
                ]
            for i, s in enumerate(sylls):
                for n in s["notes"]:
                    if (
                        prev_midi is not None
                        and n["midi"] - prev_midi >= RISE_SEMITONES
                        and "rise" not in s["cues"]
                    ):
                        s["cues"].append("rise")
                    prev_midi = n["midi"]
                if not s["notes"]:
                    continue
                last = s["notes"][-1]
                length = last["endMs"] - last["startMs"]
                if length >= HOLD_BEATS * beat_ms or length >= HOLD_MS:
                    s["cues"].append("hold")
                nxt = next((n for t in sylls[i + 1 :] for n in t["notes"]), None)
                if nxt is not None and nxt["startMs"] - last["endMs"] >= BREATH_MS:
                    s["cues"].append("breath")
            timed = [n for s in sylls for n in s["notes"]]
            out.append(
                {
                    "sectionId": section["id"],
                    "startMs": timed[0]["startMs"] if timed else None,
                    "endMs": timed[-1]["endMs"] if timed else None,
                    "syllables": sylls,
                }
            )
            li += 1
    return out


# ------------------------------------------------------------------ offset


def _onset_envelope(samples: np.ndarray, rate: int) -> np.ndarray:
    """Spectral flux every 10 ms, computed in chunks of PITCH_CHUNK frames."""
    hop = rate // 100
    x = np.asarray(samples, dtype=np.float64)
    if len(x) < 1024:
        x = np.pad(x, (0, 1024 - len(x)))
    frames = np.lib.stride_tricks.sliding_window_view(x, 1024)[::hop]
    window = np.hanning(1024)
    flux = np.zeros(len(frames))
    prev = None
    for c0 in range(0, len(frames), PITCH_CHUNK):
        mag = np.log1p(
            np.abs(np.fft.rfft(frames[c0 : c0 + PITCH_CHUNK] * window, axis=1))
        )
        if prev is not None:
            mag_all = np.vstack([prev, mag])
        else:
            mag_all = np.vstack([mag[:1], mag])
        flux[c0 : c0 + len(mag)] = np.maximum(np.diff(mag_all, axis=0), 0).sum(axis=1)
        prev = mag[-1:]
    flux[0] = 0.0
    return flux


def estimate_offset_ms(a: np.ndarray, b: np.ndarray, rate: int) -> int:
    """How much later a's events are than b's (ms, 10 ms steps)."""
    ea, eb = _onset_envelope(a, rate), _onset_envelope(b, rate)
    n = min(len(ea), len(eb))
    ea, eb = ea[:n] - ea[:n].mean(), eb[:n] - eb[:n].mean()
    best, best_lag = -np.inf, 0
    for lag in range(-MAX_OFFSET_MS // 10, MAX_OFFSET_MS // 10 + 1):
        x = ea[max(0, lag) : n + min(0, lag)]
        y = eb[max(0, -lag) : n - max(0, lag)]
        c = float(np.dot(x, y) / (np.linalg.norm(x) * np.linalg.norm(y) + 1e-12))
        if c > best:
            best, best_lag = c, lag
    return best_lag * 10


# ------------------------------------------------------------------ service


def _mono(seg: AudioSegment) -> np.ndarray:
    seg = seg.set_channels(1).set_frame_rate(ANALYSIS_RATE)
    raw = np.array(seg.get_array_of_samples(), dtype=np.float64)
    return raw / float(1 << (8 * seg.sample_width - 1))


def _align(guide: AudioSegment, offset_ms: int, length_ms: int) -> AudioSegment:
    """Guide moved earlier by offset_ms (later if negative), cut or padded to length_ms."""
    if offset_ms > 0:
        guide = guide[offset_ms:]
    elif offset_ms < 0:
        guide = (
            AudioSegment.silent(duration=-offset_ms, frame_rate=guide.frame_rate)
            + guide
        )
    if len(guide) > length_ms:
        return guide[:length_ms]
    return guide + AudioSegment.silent(
        duration=length_ms - len(guide), frame_rate=guide.frame_rate
    )


class GuideService:
    """Aligns a guide singer to the instrumental and writes its note strip."""

    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    def _load(self, tmpdir: str, key: str) -> AudioSegment:
        path = os.path.join(tmpdir, os.path.basename(key))
        self.storage.download_key_to_file(key, path)
        return AudioSegment.from_file(path)

    async def analyze(
        self,
        guide_key: str,
        reference_key: str,
        instrumental_key: str,
        words: list[dict[str, Any]],
        sections: list[dict[str, Any]],
        vocal_gender: str,
        bpm: float,
        aligned_key: str,
        notes_key: str,
    ) -> dict[str, Any]:
        if not self.storage:
            raise RuntimeError("Storage service not configured")
        with tempfile.TemporaryDirectory() as tmpdir:
            guide = self._load(tmpdir, guide_key)
            reference = self._load(tmpdir, reference_key)
            instrumental = self._load(tmpdir, instrumental_key)

            offset_ms = estimate_offset_ms(
                _mono(reference), _mono(instrumental), ANALYSIS_RATE
            )
            aligned = _align(guide, offset_ms, len(instrumental))
            aligned_path = os.path.join(tmpdir, "aligned.mp3")
            aligned.export(aligned_path, format="mp3", bitrate="192k")
            aligned_url = self.storage.upload_file(
                aligned_key, aligned_path, "audio/mpeg"
            )

            midi = track_pitch(_mono(aligned), ANALYSIS_RATE, vocal_gender)
            notes = segment_notes(midi)
            # Word times are on the guide's own clock; move them with it.
            shifted = [
                {
                    **w,
                    "start_s": w["start_s"] - offset_ms / 1000,
                    "end_s": w["end_s"] - offset_ms / 1000,
                }
                for w in words
            ]
            times, _extra = match_words(shifted, sections)
            lines = build_lines(notes, sections, times, bpm)
            voiced = midi[~np.isnan(midi)]
            range_midi = (
                [int(np.percentile(voiced, 5)), int(np.percentile(voiced, 95))]
                if len(voiced)
                else None
            )
            doc = {
                "version": 1,
                "offsetMs": offset_ms,
                "pitchShiftSemitones": 0,
                "rangeMidi": range_midi,
                "lines": lines,
            }
            notes_path = os.path.join(tmpdir, "notes.json")
            Path(notes_path).write_text(
                json.dumps(doc, ensure_ascii=False), encoding="utf-8"
            )
            notes_url = self.storage.upload_file(
                notes_key, notes_path, "application/json"
            )

        return {
            "aligned_url": aligned_url,
            "notes_url": notes_url,
            "offset_ms": offset_ms,
            "range_midi": range_midi,
            "note_count": len(notes),
            "lines_matched": sum(1 for line in lines if line["startMs"] is not None),
        }
