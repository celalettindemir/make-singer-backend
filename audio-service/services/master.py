"""Mastering service for audio processing."""

import asyncio
import logging
import math
import os
import tempfile
import time
from typing import Any

import numpy as np
from pydub import AudioSegment
from pydub.effects import compress_dynamic_range
from scipy import signal

from . import vocal_chain
from .storage import StorageService
from .take_align import ALIGN_RATE, find_shift_ms, onset_envelope, place_take
from .vocal_chain import BANDS_HZ, finalize_loudness, mix_vocal
from .vocal_ref import choose_reference

logger = logging.getLogger("uvicorn.error")

# Mastering profiles define EQ and compression settings
PROFILES = {
    "clean": {
        "eq": {"low": 0, "mid": 0, "high": 0},
        "compression_threshold": -20,
        "compression_ratio": 2.0,
        "limiter_threshold": -1.0,
        "target_lufs": -14,
    },
    "warm": {
        "eq": {"low": 2, "mid": -1, "high": -1},
        "compression_threshold": -18,
        "compression_ratio": 3.0,
        "limiter_threshold": -0.5,
        "target_lufs": -12,
    },
    "loud": {
        "eq": {"low": 1, "mid": 1, "high": 2},
        "compression_threshold": -15,
        "compression_ratio": 4.0,
        "limiter_threshold": -0.3,
        "target_lufs": -10,
    },
}


# Shelf corner frequencies for the profile EQ.
LOW_SHELF_HZ = 200
HIGH_SHELF_HZ = 8000

# Summed or equalized audio is scaled down to this peak instead of clipping.
HEADROOM_PEAK = 0.98
# Fade-out where a take is cut at its section end, so the cut does not click.
TAKE_FADE_MS = 30

_INT32_FULL_SCALE = 2**31 - 1

OUTPUT_RATE = 48000
USER_TAKE_PREFIX = "vocals/"
# Third-octave bands whose match-EQ gain is logged (Hz).
_LOG_BANDS_HZ = (250, 500, 1000, 2000, 3000, 5000, 8000, 12000)
_LOG_BAND_NAMES = ("250", "500", "1k", "2k", "3k", "5k", "8k", "12k")


def _to_float(audio: AudioSegment) -> np.ndarray:
    """Samples as float64 in [-1, 1], shaped (frames, channels)."""
    samples = np.array(audio.get_array_of_samples(), dtype=np.float64)
    samples /= float(2 ** (8 * audio.sample_width - 1))
    return samples.reshape(-1, audio.channels)


def _to_float32(audio: AudioSegment) -> np.ndarray:
    """Like _to_float but float32, for long signals where float64 doubles memory."""
    samples = np.array(audio.get_array_of_samples(), dtype=np.float32)
    samples /= np.float32(2 ** (8 * audio.sample_width - 1))
    return samples.reshape(-1, audio.channels)


def _from_float(samples: np.ndarray, frame_rate: int) -> AudioSegment:
    """32-bit segment from float samples shaped (frames, channels)."""
    clipped = np.clip(samples, -1.0, 1.0)
    data = np.round(clipped * _INT32_FULL_SCALE).astype("<i4")
    return AudioSegment(
        data.tobytes(),
        frame_rate=frame_rate,
        sample_width=4,
        channels=samples.shape[1],
    )


class MasterService:
    """Handles audio mastering operations."""

    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def process(
        self,
        stem_keys: list[str],
        mix_settings: list[dict[str, Any]],
        profile: str,
        vocal_takes: list[dict[str, Any]],
        output_key: str,
        guide_key: str | None = None,
    ) -> dict[str, Any]:
        """
        Process stems through the mastering chain.

        1. Download all stems
        2. Apply mix settings (volume, pan, mute/solo)
        3. Mix stems together
        4. Add vocal takes if provided
        5. Apply mastering chain (EQ, compression, limiting)
        6. Upload result
        """
        if not self.storage:
            raise RuntimeError("Storage service not configured")

        # The chain is CPU bound and storage I/O blocks: off the event loop,
        # or /health stalls and the liveness probe restarts the service.
        return await asyncio.to_thread(
            self._process,
            stem_keys,
            mix_settings,
            profile,
            vocal_takes,
            output_key,
            guide_key,
        )

    def _process(
        self,
        stem_keys: list[str],
        mix_settings: list[dict[str, Any]],
        profile: str,
        vocal_takes: list[dict[str, Any]],
        output_key: str,
        guide_key: str | None = None,
    ) -> dict[str, Any]:
        if profile not in PROFILES:
            profile = "clean"

        profile_settings = PROFILES[profile]

        with tempfile.TemporaryDirectory() as tmpdir:
            # Download stems (nesne anahtariyla, S3 API uzerinden)
            stems = []
            for i, stem_key in enumerate(stem_keys):
                stem_path = os.path.join(tmpdir, f"stem_{i}.wav")
                self.storage.download_key_to_file(stem_key, stem_path)
                stems.append(AudioSegment.from_file(stem_path))

            # Apply mix settings and combine
            mixed = self._mix_stems(stems, mix_settings)

            # Takes the app itself sang (guides/) are summed raw; only the
            # user's own recordings go through the vocal chain.
            user_takes = [
                t for t in vocal_takes if t["key"].startswith(USER_TAKE_PREFIX)
            ]
            raw_takes = [
                t for t in vocal_takes if not t["key"].startswith(USER_TAKE_PREFIX)
            ]
            if raw_takes:
                vocal_audio = self._load_vocals(tmpdir, raw_takes)
                if vocal_audio:
                    mixed = self._sum([mixed, vocal_audio])

            chain_s = None
            vocal_info: dict[str, float] = {}
            if user_takes:
                started = time.process_time()
                mixed, vocal_info = self._apply_vocal_chain(
                    tmpdir, mixed, user_takes, guide_key
                )
                chain_s = time.process_time() - started

            # Apply profile EQ and compression
            mastered = self._apply_mastering(mixed, profile_settings)

            # Real loudness and peak, measured on what gets written (48 kHz).
            x = _to_float(mastered).T.astype(np.float32)
            rate = mastered.frame_rate
            if rate != OUTPUT_RATE:
                g = math.gcd(OUTPUT_RATE, rate)
                x = signal.resample_poly(x, OUTPUT_RATE // g, rate // g, axis=1)
            x, lufs, peak_db = finalize_loudness(
                np.asarray(x, dtype=np.float32),
                OUTPUT_RATE,
                profile_settings["target_lufs"],
                profile_settings["limiter_threshold"],
            )
            duration = x.shape[1] / OUTPUT_RATE
            mastered = _from_float(x.T, OUTPUT_RATE)

            if user_takes:
                logger.info(
                    "vocal chain: vocal_lufs=%.1f inst_lufs=%.1f final_lufs=%.1f "
                    "peak_db=%.2f chain_s=%.1f",
                    vocal_info.get("vocal_lufs", float("nan")),
                    vocal_info.get("inst_lufs", float("nan")),
                    lufs,
                    peak_db,
                    chain_s,
                )

            # Export and upload
            output_path = os.path.join(tmpdir, "master.wav")
            mastered.export(output_path, format="wav")

            output_url = output_path
            if self.storage:
                output_url = self.storage.upload_file(
                    output_key, output_path, "audio/wav"
                )

            return {
                "output_url": output_url,
                "duration": duration,
                "peak_db": peak_db,
                "lufs": lufs,
            }

    def _apply_vocal_chain(
        self,
        tmpdir: str,
        mixed: AudioSegment,
        user_takes: list[dict[str, Any]],
        guide_key: str | None,
    ) -> tuple[AudioSegment, dict[str, float]]:
        """Run the user's takes through the C chain and mix them into the song."""
        rate = vocal_chain.RATE
        inst = np.ascontiguousarray(
            _to_float32(mixed.set_frame_rate(rate).set_channels(2)).T
        )

        takes = []
        for i, take in enumerate(user_takes):
            path = os.path.join(tmpdir, f"user_take_{i}.wav")
            self.storage.download_key_to_file(take["key"], path)
            voc = AudioSegment.from_file(path)
            voc = self._apply_take_gain(voc, take)
            mono = voc.set_channels(1).set_frame_rate(rate)
            takes.append((take, _to_float32(mono)[:, 0].copy()))

        guide44, guide16, guide_env = self._load_guide(tmpdir, guide_key)

        joined = np.concatenate(
            [signal.resample_poly(x, ALIGN_RATE // 100, rate // 100) for _, x in takes]
        ).astype(np.float32)
        kind, ref_ltas = choose_reference(joined, guide16, guide44)
        del joined

        timeline = inst.shape[1]
        placed = []
        for take, x in takes:
            start_ms = take.get("start_ms") or 0
            end_ms = take.get("end_ms")
            shift_ms, corr = 0, 0.0
            if kind == "guide":
                take16 = signal.resample_poly(x, ALIGN_RATE // 100, rate // 100)
                shift_ms, corr = find_shift_ms(take16, guide_env, start_ms)
            logger.info(
                "vocal chain: take=%s start_ms=%d shift_ms=%d corr=%.2f",
                take["key"],
                start_ms,
                shift_ms,
                corr,
            )
            start, seg = place_take(x, rate, start_ms, end_ms, shift_ms)
            placed.append((start, seg))
            timeline = max(timeline, start + len(seg))

        track = np.zeros(timeline, dtype=np.float32)
        for start, seg in placed:
            track[start : start + len(seg)] += seg
        del placed, takes

        out = vocal_chain.process_vocal(track, rate, ref_ltas)
        curve = [float(np.interp(hz, BANDS_HZ, out.curve_db)) for hz in _LOG_BANDS_HZ]
        logger.info(
            "vocal chain: ref=%s curve@%s=%s",
            kind,
            "/".join(_LOG_BAND_NAMES),
            "/".join(f"{c:.1f}" for c in curve),
        )
        mix, info = mix_vocal(out, inst, rate)
        return _from_float(mix.T, rate), info

    def _load_guide(
        self, tmpdir: str, guide_key: str | None
    ) -> tuple[np.ndarray | None, np.ndarray | None, np.ndarray | None]:
        """Guide vocal at 44.1 kHz and 16 kHz plus its onset envelope, or Nones."""
        if not guide_key:
            return None, None, None
        try:
            path = os.path.join(tmpdir, "guide.wav")
            self.storage.download_key_to_file(guide_key, path)
            seg = AudioSegment.from_file(path).set_channels(1)
            guide44 = _to_float32(seg.set_frame_rate(vocal_chain.RATE))[:, 0].copy()
            guide16 = _to_float32(seg.set_frame_rate(ALIGN_RATE))[:, 0].copy()
            return guide44, guide16, onset_envelope(guide16, ALIGN_RATE)
        except Exception as exc:  # noqa: BLE001 - any failure means "no guide"
            logger.warning("vocal chain: guide %s unusable: %s", guide_key, exc)
            return None, None, None

    @staticmethod
    def _apply_take_gain(vocal: AudioSegment, take: dict[str, Any]) -> AudioSegment:
        volume = take.get("volume", 1.0)
        if volume != 1.0:
            vocal = vocal + 20 * np.log10(max(volume, 0.001))
        gain_db = take.get("gain_db")
        if gain_db:
            vocal = vocal + gain_db
        return vocal

    def _mix_stems(
        self, stems: list[AudioSegment], mix_settings: list[dict[str, Any]]
    ) -> AudioSegment:
        """Mix stems according to settings."""
        if not stems:
            raise ValueError("No stems provided")

        # Check for solo tracks
        has_solo = any(s.get("solo", False) for s in mix_settings)

        audible = []
        for i, stem in enumerate(stems):
            settings = mix_settings[i] if i < len(mix_settings) else {}

            # Skip muted tracks
            if settings.get("mute", False):
                continue

            # If any track is soloed, skip non-solo tracks
            if has_solo and not settings.get("solo", False):
                continue

            # Apply volume
            volume = settings.get("volume", 1.0)
            if volume != 1.0:
                # Convert linear volume to dB
                volume_db = 20 * np.log10(max(volume, 0.001))
                stem = stem + volume_db

            # Apply pan (simplified stereo panning)
            pan = settings.get("pan", 0.0)
            if pan != 0.0:
                stem = stem.pan(pan)

            audible.append(stem)

        if not audible:
            return AudioSegment.silent(
                duration=max(len(s) for s in stems), frame_rate=stems[0].frame_rate
            )
        return self._sum(audible)

    def _load_vocals(
        self, tmpdir: str, vocal_takes: list[dict[str, Any]]
    ) -> AudioSegment | None:
        """Load and combine vocal takes."""
        if not vocal_takes or not self.storage:
            return None

        takes = []
        for i, take in enumerate(vocal_takes):
            vocal_path = os.path.join(tmpdir, f"vocal_{i}.wav")
            self.storage.download_key_to_file(take["key"], vocal_path)

            vocal = AudioSegment.from_file(vocal_path)

            # Apply volume
            volume = take.get("volume", 1.0)
            if volume != 1.0:
                volume_db = 20 * np.log10(max(volume, 0.001))
                vocal = vocal + volume_db

            gain_db = take.get("gain_db")
            if gain_db:
                vocal = vocal + gain_db

            takes.append(self._place_take(vocal, take))

        return self._sum(takes)

    @staticmethod
    def _place_take(vocal: AudioSegment, take: dict[str, Any]) -> AudioSegment:
        """Put a take on the song timeline.

        Frame 0 of the file is already the section start. end_ms keeps only
        the first (end_ms - start_ms) ms with a short fade-out at the cut;
        start_ms prepends that much silence. Missing fields leave the take
        untouched, so it still plays from t=0.
        """
        start_ms = take.get("start_ms") or 0
        end_ms = take.get("end_ms")
        if end_ms is not None:
            keep_ms = end_ms - start_ms
            if keep_ms < len(vocal):
                vocal = vocal[:keep_ms].fade_out(min(TAKE_FADE_MS, keep_ms))
        if start_ms > 0:
            silence = AudioSegment.silent(
                duration=start_ms, frame_rate=vocal.frame_rate
            ).set_channels(vocal.channels)
            vocal = silence + vocal
        return vocal

    @staticmethod
    def _sum(segments: list[AudioSegment]) -> AudioSegment:
        """Sum segments in floating point and keep the peak below full scale.

        pydub's overlay adds integer samples and clips, so stems that each peak
        near 0 dBFS turn into crackle when stacked. The level lost here comes
        back in the mastering chain's normalize step.
        """
        frame_rate = max(s.frame_rate for s in segments)
        channels = max(s.channels for s in segments)
        arrays = [
            _to_float(s.set_frame_rate(frame_rate).set_channels(channels))
            for s in segments
        ]
        total = np.zeros((max(len(a) for a in arrays), channels))
        for array in arrays:
            total[: len(array)] += array

        peak = np.max(np.abs(total)) if total.size else 0.0
        if peak > HEADROOM_PEAK:
            total *= HEADROOM_PEAK / peak
        return _from_float(total, frame_rate)

    def _apply_eq(self, audio: AudioSegment, settings: dict[str, Any]) -> AudioSegment:
        """Low and high shelves: the band is boosted or cut, the rest stays.

        A shelf adds (gain - 1) times the filtered band back onto the signal,
        so the mid range passes through untouched.
        """
        eq = settings.get("eq", {})
        shelves = [
            (eq.get("low", 0), lambda a: a.low_pass_filter(LOW_SHELF_HZ)),
            (eq.get("high", 0), lambda a: a.high_pass_filter(HIGH_SHELF_HZ)),
        ]
        if all(gain_db == 0 for gain_db, _ in shelves):
            return audio

        dry = _to_float(audio)
        wet = dry.copy()
        for gain_db, band_filter in shelves:
            if gain_db != 0:
                band = _to_float(band_filter(audio))[: len(dry)]
                wet[: len(band)] += (10 ** (gain_db / 20) - 1) * band

        peak = np.max(np.abs(wet)) if wet.size else 0.0
        if peak > HEADROOM_PEAK:
            wet *= HEADROOM_PEAK / peak
        return _from_float(wet, audio.frame_rate)

    def _apply_mastering(
        self, audio: AudioSegment, settings: dict[str, Any]
    ) -> AudioSegment:
        """Apply mastering chain to audio."""
        audio = self._apply_eq(audio, settings)

        # Apply compression
        threshold = settings.get("compression_threshold", -20)
        ratio = settings.get("compression_ratio", 2.0)
        audio = compress_dynamic_range(audio, threshold=threshold, ratio=ratio)

        return audio
