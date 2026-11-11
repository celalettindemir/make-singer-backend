"""Align user takes to the song's guide vocal and place them on the timeline.

Pure numpy, no storage or network.
"""

import numpy as np

from services.guide import _onset_envelope

ALIGN_RATE = 16000
MAX_LAG = 50  # frames of 10 ms
MIN_CORR = 0.25
MIN_RATIO = 1.15
PEAK_SEPARATION = 6  # lags (60 ms)
FADE_MS = 30


def _corr_at(et: np.ndarray, guide_env: np.ndarray, first: int) -> float | None:
    n = len(et)
    lo = max(0, -first)
    hi = min(n, len(guide_env) - first)
    if hi - lo < n / 2 or hi - lo < 2:
        return None
    a = et[lo:hi]
    b = guide_env[first + lo : first + hi]
    sa, sb = a.std(), b.std()
    if sa < 1e-12 or sb < 1e-12:
        return 0.0
    return float(np.mean((a - a.mean()) / sa * (b - b.mean()) / sb))


def find_shift_ms(
    take16: np.ndarray, guide_env: np.ndarray, start_ms: int
) -> tuple[int, float]:
    """Return (shift_ms, corr); shift_ms is 0 when the peak is not convincing."""
    et = _onset_envelope(take16, ALIGN_RATE)
    base = start_ms // 10
    scores: dict[int, float] = {}
    for lag in range(-MAX_LAG, MAX_LAG + 1):
        c = _corr_at(et, guide_env, base + lag)
        if c is not None:
            scores[lag] = c
    if not scores:
        return 0, 0.0
    best_lag = max(scores, key=lambda k: scores[k])
    best = scores[best_lag]
    others = [c for k, c in scores.items() if abs(k - best_lag) > PEAK_SEPARATION]
    second = max(others) if others else 0.0
    if best >= MIN_CORR and (second <= 0 or best >= MIN_RATIO * second):
        return best_lag * 10, best
    return 0, best


def place_take(
    x: np.ndarray, rate: int, start_ms: int, end_ms: int | None, shift_ms: int
) -> tuple[int, np.ndarray]:
    """Return (start sample on the timeline, trimmed/faded float32 segment)."""
    seg = np.asarray(x, dtype=np.float32)
    start = round(start_ms * rate / 1000)
    shift = round(shift_ms * rate / 1000)
    if shift < 0:
        seg = seg[-shift:]
    else:
        start += shift
    if end_ms is not None:
        keep = max(0, round(end_ms * rate / 1000) - start)
        if len(seg) > keep:
            seg = seg[:keep]
            fade = min(round(FADE_MS * rate / 1000), len(seg))
            if fade > 0:
                seg = seg.copy()
                seg[-fade:] *= np.linspace(1.0, 0.0, fade, dtype=np.float32)
    return start, seg
