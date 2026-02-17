"""Mastering service for audio processing."""

import os
import tempfile
from typing import Any

import numpy as np
from pydub import AudioSegment
from pydub.effects import normalize, compress_dynamic_range

from .storage import StorageService


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


class MasterService:
    """Handles audio mastering operations."""

    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def process(
        self,
        stem_urls: list[str],
        mix_settings: list[dict[str, Any]],
        profile: str,
        vocal_takes: list[dict[str, Any]],
        output_key: str,
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
        if profile not in PROFILES:
            profile = "clean"

        profile_settings = PROFILES[profile]

        with tempfile.TemporaryDirectory() as tmpdir:
            # Download stems
            stems = []
            for i, url in enumerate(stem_urls):
                stem_path = os.path.join(tmpdir, f"stem_{i}.wav")
                await self.storage.download_to_file(url, stem_path)
                stems.append(AudioSegment.from_file(stem_path))

            # Apply mix settings and combine
            mixed = self._mix_stems(stems, mix_settings)

            # Add vocal takes if provided
            if vocal_takes:
                vocal_audio = await self._load_vocals(tmpdir, vocal_takes)
                if vocal_audio:
                    mixed = mixed.overlay(vocal_audio)

            # Apply mastering chain
            mastered = self._apply_mastering(mixed, profile_settings)

            # Calculate metrics
            peak_db = mastered.max_dBFS
            lufs = self._calculate_lufs(mastered)
            duration = len(mastered) / 1000.0  # Convert to seconds

            # Export and upload
            output_path = os.path.join(tmpdir, "master.wav")
            mastered.export(output_path, format="wav", parameters=["-ar", "48000"])

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

    def _mix_stems(
        self, stems: list[AudioSegment], mix_settings: list[dict[str, Any]]
    ) -> AudioSegment:
        """Mix stems according to settings."""
        if not stems:
            raise ValueError("No stems provided")

        # Check for solo tracks
        has_solo = any(s.get("solo", False) for s in mix_settings)

        # Start with silence matching the longest stem
        max_length = max(len(s) for s in stems)
        mixed = AudioSegment.silent(duration=max_length)

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

            # Overlay onto mix
            mixed = mixed.overlay(stem)

        return mixed

    async def _load_vocals(
        self, tmpdir: str, vocal_takes: list[dict[str, Any]]
    ) -> AudioSegment | None:
        """Load and combine vocal takes."""
        if not vocal_takes or not self.storage:
            return None

        combined = None
        for i, take in enumerate(vocal_takes):
            vocal_path = os.path.join(tmpdir, f"vocal_{i}.wav")
            await self.storage.download_to_file(take["url"], vocal_path)

            vocal = AudioSegment.from_file(vocal_path)

            # Apply volume
            volume = take.get("volume", 1.0)
            if volume != 1.0:
                volume_db = 20 * np.log10(max(volume, 0.001))
                vocal = vocal + volume_db

            if combined is None:
                combined = vocal
            else:
                combined = combined.overlay(vocal)

        return combined

    def _apply_mastering(
        self, audio: AudioSegment, settings: dict[str, Any]
    ) -> AudioSegment:
        """Apply mastering chain to audio."""
        # Apply EQ (simplified high/mid/low shelf)
        eq = settings.get("eq", {})
        if eq.get("low", 0) != 0:
            audio = audio.low_pass_filter(200) + eq["low"]
        if eq.get("high", 0) != 0:
            audio = audio.high_pass_filter(8000) + eq["high"]

        # Apply compression
        threshold = settings.get("compression_threshold", -20)
        ratio = settings.get("compression_ratio", 2.0)
        audio = compress_dynamic_range(audio, threshold=threshold, ratio=ratio)

        # Normalize to target level
        audio = normalize(audio)

        # Apply limiter (simple peak limiting via normalize)
        limiter_threshold = settings.get("limiter_threshold", -1.0)
        if audio.max_dBFS > limiter_threshold:
            audio = audio - (audio.max_dBFS - limiter_threshold)

        return audio

    def _calculate_lufs(self, audio: AudioSegment) -> float:
        """
        Calculate integrated LUFS (simplified).

        Note: This is a simplified calculation. For accurate LUFS,
        use a dedicated library like pyloudnorm.
        """
        # Get samples as numpy array
        samples = np.array(audio.get_array_of_samples(), dtype=np.float32)

        # Normalize to -1 to 1 range
        max_val = 2 ** (audio.sample_width * 8 - 1)
        samples = samples / max_val

        # Calculate RMS
        rms = np.sqrt(np.mean(samples**2))

        # Convert to LUFS (approximate)
        if rms > 0:
            lufs = 20 * np.log10(rms) - 0.691
        else:
            lufs = -70

        return round(lufs, 1)
