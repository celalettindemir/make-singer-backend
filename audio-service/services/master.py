"""Mastering service for audio processing."""

import asyncio
import logging
import math
import os
import tempfile
import time
import wave
from typing import Any

import numpy as np
from pydub import AudioSegment
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
_CONVERT_ROWS = 1 << 20

OUTPUT_RATE = 48000
USER_TAKE_PREFIX = "vocals/"
# Third-octave bands whose match-EQ gain is logged (Hz).
_LOG_BANDS_HZ = (250, 500, 1000, 2000, 3000, 5000, 8000, 12000)
_LOG_BAND_NAMES = ("250", "500", "1k", "2k", "3k", "5k", "8k", "12k")


def _to_float(audio: AudioSegment, dtype=np.float64) -> np.ndarray:
    """Samples as floats in [-1, 1], shaped (frames, channels)."""
    samples = np.array(audio.get_array_of_samples(), dtype=dtype)
    samples /= dtype(2 ** (8 * audio.sample_width - 1))
    return samples.reshape(-1, audio.channels)


def _to_float32(audio: AudioSegment) -> np.ndarray:
    """float32 version for long signals, where float64 doubles the memory."""
    return _to_float(audio, np.float32)


def _resample(x: np.ndarray, src: int, dst: int, axis: int = 0) -> np.ndarray:
    """Polyphase resampling in float32 (anti-aliased), the only rate converter here."""
    if src != dst:
        g = math.gcd(src, dst)
        x = signal.resample_poly(x, dst // g, src // g, axis=axis)
    return np.ascontiguousarray(x, dtype=np.float32)


def _write_wav16(path: str, x: np.ndarray, rate: int) -> None:
    """Write a (channels, N) float array as 16-bit PCM, rounded and clipped at +-1."""
    with wave.open(path, "wb") as w:
        w.setnchannels(x.shape[0])
        w.setsampwidth(2)
        w.setframerate(rate)
        for a in range(0, x.shape[1], _CONVERT_ROWS):
            block = np.clip(x[:, a : a + _CONVERT_ROWS], -1.0, 1.0).astype(np.float64)
            pcm = np.round(block * 32767).astype("<i2")
            w.writeframes(np.ascontiguousarray(pcm.T).tobytes())


def _from_float(samples: np.ndarray, frame_rate: int) -> AudioSegment:
    """32-bit segment from float samples shaped (frames, channels)."""
    data = np.empty(samples.shape, dtype="<i4")
    for a in range(0, len(samples), _CONVERT_ROWS):  # blocks keep the temporaries small
        block = np.clip(samples[a : a + _CONVERT_ROWS], -1.0, 1.0).astype(np.float64)
        data[a : a + _CONVERT_ROWS] = np.round(block * _INT32_FULL_SCALE)
    return AudioSegment(
        data.tobytes(),
        frame_rate=frame_rate,
        sample_width=4,
        channels=samples.shape[1],
    )


def compress_dynamic_range(
    seg: AudioSegment,
    threshold: float = -20.0,
    ratio: float = 4.0,
    attack: float = 5.0,
    release: float = 50.0,
) -> AudioSegment:
    """pydub's compress_dynamic_range with the same math, in numpy.

    pydub keeps one bytes object per frame and loops over them in Python: it
    needs about 1.5 GB for a 4-minute song. Here the window RMS comes from a
    cumulative sum, only the attenuation recursion runs frame by frame, and the
    gain is applied in blocks.
    """
    x = np.frombuffer(seg.raw_data, dtype=f"<i{seg.sample_width}")
    ch = seg.channels
    frames = len(x) // ch
    x = x[: frames * ch].reshape(frames, ch)
    thresh_rms = float(seg.max_possible_amplitude) * 10 ** (threshold / 20)
    look = int(seg.frame_count(ms=attack))
    attack_frames = seg.frame_count(ms=attack)
    release_frames = seg.frame_count(ms=release)

    # Energy of the look-back window [i - look, i). Like pydub, the window starts
    # at frame 0 for i < look (partial window); it is empty (RMS 0) at i = 0.
    csum = np.zeros(frames + 1)
    for a in range(0, frames, _CONVERT_ROWS):
        block = x[a : a + _CONVERT_ROWS].astype(np.float64)
        csum[a + 1 : a + 1 + len(block)] = (block**2).sum(axis=1)
    np.cumsum(csum, out=csum)
    rms = np.zeros(frames)
    head = min(look, frames)
    if head > 1:
        rms[1:head] = np.sqrt(csum[1:head] / (np.arange(1, head) * ch))
    if look > 0 and frames > look:
        rms[look:] = np.sqrt((csum[look:frames] - csum[: frames - look]) / (look * ch))
    del csum
    np.floor(rms, out=rms)  # audioop.rms returns an integer
    over = rms > thresh_rms
    with np.errstate(divide="ignore", invalid="ignore"):
        db_over = np.where(over, 20 * np.log10(rms / thresh_rms), 0.0)
    max_att = (1 - 1.0 / ratio) * np.maximum(db_over, 0)
    del db_over

    atten = np.zeros(frames, dtype=np.float32)
    current = 0.0
    for a in range(0, frames, _CONVERT_ROWS):
        b = min(a + _CONVERT_ROWS, frames)
        hot = over[a:b].tolist()
        top = max_att[a:b].tolist()
        out = [0.0] * (b - a)
        for k in range(b - a):
            m = top[k]
            if hot[k] and current <= m:
                current = min(current + m / attack_frames, m)
            else:
                current = max(current - m / release_frames, 0.0)
            out[k] = current
        atten[a:b] = out
    del rms, over, max_att

    gain = np.power(10.0, -atten.astype(np.float64) / 20)
    del atten
    result = np.empty(x.shape, dtype=x.dtype)
    full = float(2 ** (8 * seg.sample_width - 1))
    for a in range(0, frames, _CONVERT_ROWS):
        block = x[a : a + _CONVERT_ROWS] * gain[a : a + _CONVERT_ROWS, None]
        np.clip(block, -full, full - 1, out=block)
        result[a : a + _CONVERT_ROWS] = block  # truncates toward zero like audioop
    return seg._spawn(data=result.tobytes())


# One master at a time: a long song peaks near 1.35 GB on the 2 GiB pod.
_MASTER_SLOT = asyncio.Semaphore(1)


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
        async with _MASTER_SLOT:
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
            del stems

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
                started = time.thread_time()
                holder = [mixed]
                del mixed
                mixed, vocal_info = self._apply_vocal_chain(
                    tmpdir, holder, user_takes, guide_key
                )
                chain_s = time.thread_time() - started

            # Apply profile EQ and compression
            mastered = self._apply_mastering(mixed, profile_settings)

            # Real loudness and peak, measured on what gets written (48 kHz).
            x = _to_float32(mastered).T
            rate = mastered.frame_rate
            del mastered, mixed
            x = _resample(x, rate, OUTPUT_RATE, axis=1)
            x, lufs, peak_db = finalize_loudness(
                np.asarray(x, dtype=np.float32),
                OUTPUT_RATE,
                profile_settings["target_lufs"],
                profile_settings["limiter_threshold"],
            )
            duration = x.shape[1] / OUTPUT_RATE

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
            _write_wav16(output_path, x, OUTPUT_RATE)
            del x

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
        mixed_holder: list[AudioSegment],
        user_takes: list[dict[str, Any]],
        guide_key: str | None,
    ) -> tuple[AudioSegment, dict[str, float]]:
        """Run the user's takes through the C chain and mix them into the song."""
        rate = vocal_chain.RATE
        # The caller hands the song over in a list so it can be freed here.
        song = mixed_holder.pop()
        inst = _resample(_to_float32(song.set_channels(2)).T, song.frame_rate, rate, 1)
        del song
        inst = np.ascontiguousarray(inst)

        takes = []
        for i, take in enumerate(user_takes):
            path = os.path.join(tmpdir, f"user_take_{i}.wav")
            self.storage.download_key_to_file(take["key"], path)
            voc = AudioSegment.from_file(path)
            voc = self._apply_take_gain(voc, take)
            mono = _to_float32(voc.set_channels(1))[:, 0]
            takes.append((take, _resample(mono, voc.frame_rate, rate)))
            del voc, mono

        guide44, guide16, guide_env = self._load_guide(tmpdir, guide_key)

        joined = np.concatenate(
            [_resample(x, rate, ALIGN_RATE) for _, x in takes]
        ).astype(np.float32)
        kind, ref_ltas = choose_reference(joined, guide16, guide44)
        del joined, guide16, guide44

        timeline = inst.shape[1]
        placed = []
        for take, x in takes:
            start_ms = take.get("start_ms") or 0
            end_ms = take.get("end_ms")
            shift_ms, corr = 0, 0.0
            if kind == "guide":
                take16 = _resample(x, rate, ALIGN_RATE)
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
        del placed, takes, x, seg
        take16 = None

        out = vocal_chain.process_vocal(track, rate, ref_ltas)
        # Log-frequency interpolation, like apply_curve and the built-in curves.
        curve = [
            float(np.interp(np.log(hz), np.log(BANDS_HZ), out.curve_db))
            for hz in _LOG_BANDS_HZ
        ]
        logger.info(
            "vocal chain: ref=%s curve@%s=%s",
            kind,
            "/".join(_LOG_BAND_NAMES),
            "/".join(f"{c:.1f}" for c in curve),
        )
        del track
        mix, info = mix_vocal(out, inst, rate, inplace=True)
        del out, inst
        # The vocal sits +2 LU over the instrumental, so the sum can pass full
        # scale. Scale down like _sum does; finalize_loudness restores the level.
        peak = float(np.max(np.abs(mix))) if mix.size else 0.0
        if peak > HEADROOM_PEAK:
            mix *= np.float32(HEADROOM_PEAK / peak)
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
            native = _to_float32(seg)[:, 0]
            guide44 = _resample(native, seg.frame_rate, vocal_chain.RATE)
            guide16 = _resample(native, seg.frame_rate, ALIGN_RATE)
            del native
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
        back in finalize_loudness, which sets the final loudness.
        """
        frame_rate = max(s.frame_rate for s in segments)
        channels = max(s.channels for s in segments)
        # One segment at a time, float32: a 4-minute song is 90 MB per copy.
        converted = [
            s.set_frame_rate(frame_rate).set_channels(channels) for s in segments
        ]
        total = np.zeros(
            (max(int(c.frame_count()) for c in converted), channels), np.float32
        )
        for i in range(len(converted)):
            array = _to_float32(converted[i])
            converted[i] = None
            total[: len(array)] += array
            del array

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

        dry = _to_float32(audio)
        wet = dry.copy()
        for gain_db, band_filter in shelves:
            if gain_db != 0:
                band = _to_float32(band_filter(audio))[: len(dry)]
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
