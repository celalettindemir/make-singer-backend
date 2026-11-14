"""Source separation: splits a track into vocal/drums/bass/rest with Demucs.

torch and demucs are imported only inside functions, so importing this module
(and running the default test suite) never needs them. The model loader and the
apply function are injectable for the same reason.
"""

import asyncio
import logging
import os
import subprocess
import tempfile
import threading
from collections.abc import Callable
from typing import Any

import numpy as np
from pydub import AudioSegment
from pydub.exceptions import CouldntDecodeError
from scipy.io import wavfile

from .storage import StorageService

logger = logging.getLogger("audio-service.separation")

SAMPLE_RATE = 44100
MP3_BITRATE = "256k"

# Demucs source name -> instrument value; the output order is fixed (vocal
# first, R-MST-02 relies on it).
STEM_MAP = {"vocals": "vocal", "drums": "drums", "bass": "bass", "other": "rest"}
STEM_ORDER = ["vocal", "drums", "bass", "rest"]


class SeparationUnreadable(Exception):
    """The file could not be decoded as audio, or it is empty."""


def _demucs_uygula(model, girdi: np.ndarray, **kwargs) -> np.ndarray:
    """Runs demucs apply_model on a (1, C, N) numpy array, returns (1, S, C, N)."""
    import torch
    from demucs.apply import apply_model

    with torch.no_grad():
        cikti = apply_model(model, torch.from_numpy(girdi), **kwargs)
    return cikti.cpu().numpy()


def demucs_yukle():
    """Loads htdemucs once; torch thread count comes from TORCH_THREADS."""
    import torch
    from demucs.pretrained import get_model

    torch.set_num_threads(int(os.getenv("TORCH_THREADS", "2")))
    model = get_model("htdemucs")
    model.eval()
    return model


def ayir(
    dalga: np.ndarray, model, uygula: Callable[..., Any] | None = None
) -> dict[str, np.ndarray]:
    """Separates a (C, N) float32 waveform into stems named by instrument."""
    dalga = np.asarray(dalga, dtype=np.float32)
    if dalga.shape[0] == 1:
        dalga = np.repeat(dalga, 2, axis=0)
    ref = dalga.mean(0)
    ort, std = float(ref.mean()), float(ref.std())
    if std < 1e-8:
        std = 1.0
    normal = ((dalga - ort) / std).astype(np.float32)

    if uygula is None:
        uygula = _demucs_uygula
    cikti = uygula(
        model,
        normal[None],
        device="cpu",
        shifts=0,
        split=True,
        overlap=0.25,
        progress=False,
    )[0]
    # In place: two stem-sized temporaries less on a long track.
    cikti *= std
    cikti += ort
    sonuc = {
        STEM_MAP[ad]: np.asarray(cikti[i], dtype=np.float32)
        for i, ad in enumerate(model.sources)
    }
    return {ad: sonuc[ad] for ad in STEM_ORDER}


def _coz(yol: str) -> np.ndarray:
    """Decodes any audio file to (2, N) float32 at 44.1 kHz."""
    try:
        ses = AudioSegment.from_file(yol)
    except (CouldntDecodeError, IndexError) as e:
        raise SeparationUnreadable(str(e)) from e
    ses = ses.set_frame_rate(SAMPLE_RATE).set_channels(2).set_sample_width(2)
    if len(ses) <= 0:
        raise SeparationUnreadable("empty audio")
    ornekler = np.array(ses.get_array_of_samples(), dtype=np.float32) / 32768.0
    return ornekler.reshape(-1, 2).T.copy()


def _mp3_yaz(dalga: np.ndarray, mp3_yolu: str) -> None:
    """Writes a temporary wav, then encodes it to 256k stereo mp3 with ffmpeg."""
    wav_yolu = mp3_yolu + ".wav"
    wavfile.write(wav_yolu, SAMPLE_RATE, np.clip(dalga.T, -1.0, 1.0))
    subprocess.run(
        [
            "ffmpeg", "-y", "-loglevel", "error", "-i", wav_yolu,
            "-ar", str(SAMPLE_RATE), "-ac", "2", "-b:a", MP3_BITRATE, mp3_yolu,
        ],
        check=True,
    )  # fmt: skip
    os.remove(wav_yolu)


class SeparationService:
    def __init__(
        self,
        storage: StorageService | None,
        model_yukle: Callable[[], Any] | None = None,
        threads: int = 2,
        uygula: Callable[..., Any] | None = None,
    ):
        self.storage = storage
        self._model_yukle = model_yukle or demucs_yukle
        self.threads = threads
        self._uygula = uygula
        self._model = None
        self._model_kilit = threading.Lock()
        # One job at a time: demucs is CPU bound and memory hungry.
        self._sira = asyncio.Semaphore(1)

    def load(self):
        """Loads the model once (thread safe); later calls return it."""
        with self._model_kilit:
            if self._model is None:
                self._model = self._model_yukle()
            return self._model

    async def separate(self, input_key: str, output_prefix: str) -> dict:
        if not self.storage:
            raise RuntimeError("Storage service not configured")
        async with self._sira:
            return await asyncio.to_thread(self._separate, input_key, output_prefix)

    def _separate(self, input_key: str, output_prefix: str) -> dict:
        model = self.load()
        uzanti = os.path.splitext(input_key)[1] or ".wav"
        with tempfile.TemporaryDirectory() as tmpdir:
            giris = os.path.join(tmpdir, "giris" + uzanti)
            self.storage.download_key_to_file(input_key, giris)
            dalga = _coz(giris)
            sure_ms = round(dalga.shape[1] * 1000 / SAMPLE_RATE)
            stemler = ayir(dalga, model, self._uygula)
            sonuc = []
            for ad in STEM_ORDER:
                mp3 = os.path.join(tmpdir, ad + ".mp3")
                _mp3_yaz(stemler[ad], mp3)
                anahtar = f"{output_prefix}{ad}.mp3"
                self.storage.upload_file(anahtar, mp3, "audio/mpeg")
                sonuc.append({"instrument": ad, "key": anahtar})
        logger.info("separated %s: duration_ms=%d", input_key, sure_ms)
        return {"duration_ms": sure_ms, "stems": sonuc}
