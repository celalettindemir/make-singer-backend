"""Key estimate: chroma from an STFT correlated with Krumhansl-Kessler profiles.

ton_tahmini is a pure function (numpy only, no ffmpeg). It returns None when
the best key is weak (r < 0.6) or ambiguous (best - second < 0.03).
"""

import numpy as np

ANALIZ_HIZI = 22050
PENCERE = 4096
ADIM = 2048
EN_DUSUK_HZ = 55.0
EN_YUKSEK_HZ = 2000.0
EN_DUSUK_R = 0.6
EN_DUSUK_FARK = 0.03

TONLAR = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"]
KK_MAJOR = np.array(
    [6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88]
)
KK_MINOR = np.array(
    [6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17]
)


def _kroma(ornekler: np.ndarray, ornek_hizi: int) -> np.ndarray:
    """Time-summed 12-bin chroma vector (index 0 = C)."""
    kroma = np.zeros(12)
    if len(ornekler) < PENCERE:
        return kroma
    cerceveler = np.lib.stride_tricks.sliding_window_view(ornekler, PENCERE)[::ADIM]
    genlik = np.abs(np.fft.rfft(cerceveler * np.hanning(PENCERE), axis=1)).sum(axis=0)
    frekanslar = np.fft.rfftfreq(PENCERE, 1.0 / ornek_hizi)
    secili = (frekanslar >= EN_DUSUK_HZ) & (frekanslar <= EN_YUKSEK_HZ)
    midi = np.rint(69 + 12 * np.log2(frekanslar[secili] / 440.0)).astype(int)
    np.add.at(kroma, midi % 12, genlik[secili])
    return kroma


def ton_tahmini(ornekler: np.ndarray, ornek_hizi: int) -> tuple[str, str] | None:
    """Mono -1..1 samples to (tonic, "major"|"minor"); None if not confident."""
    kroma = _kroma(np.asarray(ornekler, dtype=np.float64), ornek_hizi)
    if not np.any(kroma) or np.std(kroma) == 0:
        return None

    adaylar: list[tuple[float, str, str]] = []
    for olcek, profil in (("major", KK_MAJOR), ("minor", KK_MINOR)):
        for i, tonik in enumerate(TONLAR):
            r = float(np.corrcoef(kroma, np.roll(profil, i))[0, 1])
            adaylar.append((r, tonik, olcek))
    adaylar.sort(reverse=True)

    (en_iyi, tonik, olcek), (ikinci, _, _) = adaylar[0], adaylar[1]
    if en_iyi < EN_DUSUK_R or en_iyi - ikinci < EN_DUSUK_FARK:
        return None
    return tonik, olcek
