"""Render edilen sarkinin gercek temposu.

Suno istenen BPM'e uymaz; mobil metronom ve geri sayim bu olcumu kullanir.
tempo_tahmini saf fonksiyondur (numpy, ffmpeg yok): spektral aki ile vurus
baslangiclarini cikarir, otokorelasyonda en guclu periyodu arar.
"""

import os
import tempfile
from typing import Any

import numpy as np
from pydub import AudioSegment

from .analiz import mono_ornekler
from .storage import StorageService

ANALIZ_HIZI = 22050
ADIM_SN = 0.01
EN_DUSUK_BPM = 60
EN_YUKSEK_BPM = 180
# Sarkilarin cogu bu civarda; ikiye katlanmis/yarilanmis tempo arasinda
# kararsiz kalindiginda olcek buna yakin olani secer.
ONCELIKLI_BPM = 110
EN_KISA_SN = 4.0


def _baslangic_gucu(ornekler: np.ndarray, ornek_hizi: int) -> np.ndarray:
    """Pencereli log genlik spektrumunun pozitif farki (spektral aki)."""
    adim = max(1, round(ADIM_SN * ornek_hizi))
    pencere = 4 * adim
    adet = (len(ornekler) - pencere) // adim
    if adet <= 1:
        return np.zeros(0)
    cerceveler = np.lib.stride_tricks.sliding_window_view(ornekler, pencere)[::adim][
        :adet
    ]
    spektrum = np.log1p(
        np.abs(np.fft.rfft(cerceveler * np.hanning(pencere), axis=1)) * 100
    )
    return np.maximum(np.diff(spektrum, axis=0), 0).sum(axis=1)


def tempo_tahmini(ornekler: np.ndarray, ornek_hizi: int) -> float | None:
    """Mono -1..1 orneklerden BPM; ritim bulunamazsa None."""
    if len(ornekler) < EN_KISA_SN * ornek_hizi:
        return None
    aki = _baslangic_gucu(np.asarray(ornekler, dtype=np.float64), ornek_hizi)
    aki = aki - aki.mean()
    if aki.size == 0 or not np.any(aki):
        return None

    boy = 1 << int(np.ceil(np.log2(2 * len(aki))))
    spektrum = np.fft.rfft(aki, boy)
    oto = np.fft.irfft(spektrum * np.conj(spektrum))[: len(aki)]
    if oto[0] <= 0:
        return None
    oto = oto / oto[0]

    kare_hizi = 1.0 / ADIM_SN
    gecikmeler = np.arange(
        int(kare_hizi * 60 / EN_YUKSEK_BPM), int(kare_hizi * 60 / EN_DUSUK_BPM) + 1
    )
    gecikmeler = gecikmeler[2 * gecikmeler < len(oto)]
    if gecikmeler.size == 0:
        return None
    # Bir vurus periyodu iki katinda da tekrar eder; tek basina olcu ya da
    # yarim vurus periyodu bu toplamda geride kalir.
    puan = oto[gecikmeler] + 0.5 * oto[2 * gecikmeler]
    bpmler = 60 * kare_hizi / gecikmeler
    puan = puan * np.exp(-0.5 * np.log2(bpmler / ONCELIKLI_BPM) ** 2)
    en_iyi = int(np.argmax(puan))
    if puan[en_iyi] <= 0:
        return None

    gecikme = float(gecikmeler[en_iyi])
    if 0 < en_iyi < len(puan) - 1:
        a, b, c = puan[en_iyi - 1], puan[en_iyi], puan[en_iyi + 1]
        payda = a - 2 * b + c
        if payda != 0:
            gecikme += 0.5 * (a - c) / payda
    return round(60 * kare_hizi / gecikme, 1)


class TempoService:
    """R2'deki bir sarkinin temposunu olcer."""

    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def olc(self, input_key: str) -> dict[str, Any]:
        if not self.storage:
            raise RuntimeError("Storage service not configured")

        uzanti = os.path.splitext(input_key)[1] or ".mp3"
        with tempfile.TemporaryDirectory() as tmpdir:
            giris = os.path.join(tmpdir, "giris" + uzanti)
            self.storage.download_key_to_file(input_key, giris)
            ses = AudioSegment.from_file(giris)

        ses = ses.set_channels(1).set_frame_rate(ANALIZ_HIZI)
        return {"bpm": tempo_tahmini(mono_ornekler(ses), ANALIZ_HIZI)}
