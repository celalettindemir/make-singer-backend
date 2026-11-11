"""Align user takes to the song's guide vocal and place them on the timeline.

Pure numpy, no storage or network.
"""

import numpy as np
from scipy import sparse

ALIGN_RATE = 16000
ENV_NFFT = 1024
ENV_MELS = 64
ENV_TOP_DB = 60.0
ENV_MAX_HZ = 8000.0
MAX_LAG = 50  # frames of 10 ms
MIN_CORR = 0.25
MIN_RATIO = 1.15
PEAK_SEPARATION = 15  # lags (150 ms): the log-mel peak is ~150 ms wide
FADE_MS = 30


def _mel_filters(rate: int) -> np.ndarray:
    def mel(f):
        return 2595.0 * np.log10(1.0 + f / 700.0)

    def inv(m):
        return 700.0 * (10.0 ** (m / 2595.0) - 1.0)

    pts = inv(np.linspace(0.0, mel(ENV_MAX_HZ), ENV_MELS + 2))
    fr = np.fft.rfftfreq(ENV_NFFT, 1.0 / rate)
    fb = np.zeros((ENV_MELS, len(fr)))
    for i in range(ENV_MELS):
        lo, mid, hi = pts[i : i + 3]
        fb[i] = np.maximum(
            0.0, np.minimum((fr - lo) / (mid - lo), (hi - fr) / (hi - mid))
        )
    return fb


def onset_envelope(x16: np.ndarray, rate: int = ALIGN_RATE) -> np.ndarray:
    """Mel spectral flux every 10 ms, on a log scale limited to ENV_TOP_DB.

    guide.py's linear-magnitude flux is dominated by broadband noise and gives a
    300 ms wide correlation hump on phone takes; the log-mel flux has a peak.
    """
    hop = rate // 100
    x = np.asarray(x16, dtype=np.float32)
    if len(x) < ENV_NFFT:
        x = np.pad(x, (0, ENV_NFFT - len(x)))
    frames = np.lib.stride_tricks.sliding_window_view(x, ENV_NFFT)[::hop]
    window = np.hanning(ENV_NFFT).astype(np.float32)
    # Sparse: a dense matmul would wake every BLAS thread and burn CPU time.
    fb = sparse.csr_matrix(_mel_filters(rate).astype(np.float32))
    chunks = []
    for c0 in range(0, len(frames), 512):
        spec = np.abs(np.fft.rfft(frames[c0 : c0 + 512] * window, axis=1)) ** 2
        chunks.append(10.0 * np.log10(np.maximum(np.asarray(fb @ spec.T).T, 1e-10)))
    db = np.concatenate(chunks)
    db = np.maximum(db, db.max() - ENV_TOP_DB)
    flux = np.maximum(np.diff(db, axis=0), 0).mean(axis=1)
    return np.concatenate([[0.0], flux]).astype(np.float64)


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
    et = onset_envelope(take16, ALIGN_RATE)
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
