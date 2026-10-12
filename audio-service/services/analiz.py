"""Kesfet yayinlari icin dalga formu ve 30 sn onizleme uretimi.

Saf fonksiyonlar (tepe_degerleri, en_yuksek_rms_penceresi) numpy dizisi
uzerinde calisir ve ffmpeg istemez; AnalizService dosyayi R2'den indirir,
pydub ile cozer ve onizlemeyi mp3 olarak geri yukler.
"""

import os
import tempfile
from typing import Any

import numpy as np
from pydub import AudioSegment

from .storage import StorageService

ONIZLEME_BIT_HIZI = "192k"


def tepe_degerleri(ornekler: np.ndarray, adet: int) -> list[float]:
    """Mono ornekleri `adet` esit parcaya boler, her parcanin mutlak tepe
    degerini alir ve en yuksek tepeye gore 0-1 araligina normalize eder.

    Sessiz ya da bos girdide `adet` sifir doner; dizi her zaman `adet`
    uzunlugundadir.
    """
    if adet <= 0:
        return []
    mutlak = np.abs(np.asarray(ornekler, dtype=np.float64))
    if mutlak.size == 0:
        return [0.0] * adet
    parcalar = np.array_split(mutlak, adet)
    tepeler = np.array([p.max() if p.size else 0.0 for p in parcalar])
    en_yuksek = tepeler.max()
    if en_yuksek <= 0:
        return [0.0] * adet
    return [float(round(t / en_yuksek, 4)) for t in tepeler]


def en_yuksek_rms_penceresi(
    ornekler: np.ndarray, ornek_hizi: int, pencere_sn: float
) -> tuple[int, int]:
    """Enerjisi (karelerin toplami, yani RMS) en yuksek `pencere_sn`
    uzunlugundaki pencerenin [baslangic, bitis) ornek indekslerini doner.

    Parca pencereden kisaysa butun parca doner. Kumulatif toplamla O(n).
    """
    n = len(ornekler)
    pencere = round(pencere_sn * ornek_hizi)
    if pencere <= 0 or n <= pencere:
        return 0, n
    kareler = np.square(np.asarray(ornekler, dtype=np.float64))
    kumulatif = np.concatenate(([0.0], np.cumsum(kareler)))
    enerjiler = kumulatif[pencere:] - kumulatif[:-pencere]
    baslangic = int(np.argmax(enerjiler))
    return baslangic, baslangic + pencere


def mono_ornekler(ses: AudioSegment) -> np.ndarray:
    """AudioSegment'i -1..1 araliginda mono float diziye cevirir."""
    ham = np.array(ses.get_array_of_samples(), dtype=np.float64)
    if ses.channels > 1:
        ham = ham.reshape((-1, ses.channels)).mean(axis=1)
    olcek = float(1 << (8 * ses.sample_width - 1))
    return ham / olcek


class AnalizService:
    """Masterdan dalga formu ve onizleme uretir."""

    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def analiz(
        self,
        input_key: str,
        preview_key: str,
        peak_count: int,
        preview_seconds: int,
    ) -> dict[str, Any]:
        if not self.storage:
            raise RuntimeError("Storage service not configured")

        uzanti = os.path.splitext(input_key)[1] or ".wav"
        with tempfile.TemporaryDirectory() as tmpdir:
            giris = os.path.join(tmpdir, "giris" + uzanti)
            self.storage.download_key_to_file(input_key, giris)
            ses = AudioSegment.from_file(giris)

            ornekler = mono_ornekler(ses)
            tepeler = tepe_degerleri(ornekler, peak_count)
            baslangic, bitis = en_yuksek_rms_penceresi(
                ornekler, ses.frame_rate, preview_seconds
            )
            bas_ms = int(baslangic * 1000 / ses.frame_rate)
            bit_ms = int(bitis * 1000 / ses.frame_rate)
            onizleme = ses[bas_ms:bit_ms]

            cikis = os.path.join(tmpdir, "onizleme.mp3")
            onizleme.export(cikis, format="mp3", bitrate=ONIZLEME_BIT_HIZI)
            preview_url = self.storage.upload_file(preview_key, cikis, "audio/mpeg")

            return {
                "peaks": tepeler,
                "duration": len(ses) / 1000.0,
                "preview_url": preview_url,
            }
