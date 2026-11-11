"""Server-side vocal chain building blocks: spectrum measurement and match EQ.

Pure numpy/scipy DSP, no storage or network. Signals are mono float arrays at RATE.
"""

import numpy as np
from scipy import signal

RATE = 44100

# Third-octave centers, 63 Hz - 16 kHz (27 bands).
BANDS_HZ = 1000.0 * 2.0 ** (np.arange(-14, 13) / 3.0)

ACTIVE_WIN_S = 0.4
ACTIVE_FRACTION = 0.2
ACTIVE_FLOOR_DBFS = -40
LOUD_PERCENTILE = 60
REF_BAND = (500, 1500)
APPLY_BAND = (150, 12000)
CLAMP_DB = (-10, 12)
FIR_TAPS = 4097

_FRAME = 8192
_HOP = _FRAME // 2
_SILENT_DB = -120.0
_CHUNK = 256  # frames per FFT batch, keeps memory bounded


def movavg(x: np.ndarray, w: int) -> np.ndarray:
    """Centered moving average in O(N) via a cumulative sum; same length as x."""
    n = len(x)
    c = np.concatenate([[0.0], np.cumsum(x, dtype=np.float64)])
    h = w // 2
    idx = np.arange(n)
    i = np.clip(idx - h, 0, n)
    j = np.clip(idx + w - h, 0, n)
    return (c[j] - c[i]) / max(w, 1)


def active_mask(x: np.ndarray, rate: int) -> np.ndarray:
    """True where the signal is 'sung': loud samples dominate a 400 ms window."""
    loud = np.abs(x) > 10 ** (ACTIVE_FLOOR_DBFS / 20)
    win = max(1, int(ACTIVE_WIN_S * rate))
    return movavg(loud.astype(np.float64), win) > ACTIVE_FRACTION


def _frames(x: np.ndarray, start: int, stop: int, window: np.ndarray) -> np.ndarray:
    starts = np.arange(start, stop) * _HOP
    return x[starts[:, None] + np.arange(_FRAME)[None, :]] * window


def _band_edges(rate: int) -> tuple[np.ndarray, np.ndarray]:
    freqs = np.fft.rfftfreq(_FRAME, 1.0 / rate)
    lo = np.searchsorted(freqs, BANDS_HZ / 2 ** (1 / 6), side="left")
    hi = np.searchsorted(freqs, BANDS_HZ * 2 ** (1 / 6), side="right")
    # A band narrower than one bin falls back to the nearest bin.
    nearest = np.clip(np.round(BANDS_HZ * _FRAME / rate).astype(int), 0, len(freqs) - 1)
    empty = hi <= lo
    lo = np.where(empty, nearest, lo)
    hi = np.where(empty, nearest + 1, hi)
    return lo, hi


def ltas_db(x: np.ndarray, rate: int) -> np.ndarray:
    """Long-term average spectrum (dB per BANDS_HZ band) of the loud frames.

    8192-sample Hann frames at half hop; frames above the 60th RMS percentile
    are kept and their power spectra are summed frame by frame.
    """
    x = np.asarray(x, dtype=np.float64)
    if len(x) < _FRAME:
        x = np.pad(x, (0, _FRAME - len(x)))
    n_frames = (len(x) - _FRAME) // _HOP + 1
    window = np.hanning(_FRAME)

    rms = np.empty(n_frames)
    for a in range(0, n_frames, _CHUNK):
        b = min(a + _CHUNK, n_frames)
        rms[a:b] = np.sqrt(np.mean(_frames(x, a, b, window) ** 2, axis=1))
    if rms.max() < 1e-9:
        return np.full(len(BANDS_HZ), _SILENT_DB)
    thr = np.percentile(rms, LOUD_PERCENTILE)
    keep = rms > thr
    if not keep.any():  # constant-level input: every frame ties
        keep = rms >= thr

    power = np.zeros(_FRAME // 2 + 1)
    count = 0
    for a in range(0, n_frames, _CHUNK):
        b = min(a + _CHUNK, n_frames)
        sel = keep[a:b]
        if not sel.any():
            continue
        fr = _frames(x, a, b, window)[sel]
        power += (np.abs(np.fft.rfft(fr, axis=1)) ** 2).sum(axis=0)
        count += int(sel.sum())
    power /= count * np.sum(window**2)

    lo, hi = _band_edges(rate)
    c = np.concatenate([[0.0], np.cumsum(power)])
    band = (c[hi] - c[lo]) / (hi - lo)
    return np.maximum(10 * np.log10(band + 1e-20), _SILENT_DB)


def match_curve_db(voc_ltas: np.ndarray, ref_ltas: np.ndarray) -> np.ndarray:
    """Per-band correction (dB) that moves the vocal spectrum toward the reference."""
    curve = np.asarray(ref_ltas, dtype=np.float64) - np.asarray(
        voc_ltas, dtype=np.float64
    )
    center = (BANDS_HZ >= REF_BAND[0]) & (BANDS_HZ <= REF_BAND[1])
    curve = curve - np.median(curve[center])
    inside = np.flatnonzero((BANDS_HZ >= APPLY_BAND[0]) & (BANDS_HZ <= APPLY_BAND[1]))
    curve[BANDS_HZ < APPLY_BAND[0]] = curve[inside[0]]
    curve[BANDS_HZ > APPLY_BAND[1]] = curve[inside[-1]]
    return np.clip(curve, CLAMP_DB[0], CLAMP_DB[1])


def apply_curve(x: np.ndarray, rate: int, curve_db: np.ndarray) -> np.ndarray:
    """Apply a per-band dB curve with a linear-phase FIR (log-frequency interpolation)."""
    grid = np.linspace(0.0, rate / 2, 2049)
    gain_db = np.empty_like(grid)
    gain_db[1:] = np.interp(np.log(grid[1:]), np.log(BANDS_HZ), curve_db)
    gain_db[0] = curve_db[0]
    gain_db[-1] = np.interp(np.log(grid[-1]), np.log(BANDS_HZ), curve_db)
    taps = signal.firwin2(FIR_TAPS, grid / (rate / 2), 10 ** (gain_db / 20))
    return signal.fftconvolve(np.asarray(x, dtype=np.float64), taps, "same").astype(
        np.float32
    )
