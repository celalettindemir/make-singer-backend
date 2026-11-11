"""Server-side vocal chain building blocks: spectrum measurement and match EQ.

Pure numpy/scipy DSP, no storage or network. Signals are mono float arrays at RATE.
"""

from dataclasses import dataclass

import numpy as np
import pyloudnorm
from pedalboard import (
    Compressor,
    Delay,
    HighpassFilter,
    Limiter,
    LowpassFilter,
    NoiseGate,
    PeakFilter,
    Pedalboard,
    Reverb,
)
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

INPUT_RMS_DBFS = -18
HPF_HZ = 110
GATE = {"threshold_db": -32, "ratio": 6, "attack_ms": 2, "release_ms": 150}
COMP_1 = {"threshold_db": -22, "ratio": 4, "attack_ms": 4, "release_ms": 70}
COMP_2 = {"threshold_db": -14, "ratio": 2.5, "attack_ms": 15, "release_ms": 150}
DEESS_HZ = 6000
DEESS = {"threshold_db": -32, "ratio": 6, "attack_ms": 1, "release_ms": 40}
EXCITER_SPLIT_HZ = 2500
EXCITER_DRIVE_PEAK = 4.0
EXCITER_HPF_HZ = 4500
EXCITER_DB = -17
REVERB_DB = -13
SLAP_DB = -19
SLAP_DELAY_S = 0.115
FRONT_LU = 2.0
INST_BELL = (2500, -2.5, 0.8)
DUCK_MAX_DB = 1.5
DUCK_ENV_S = 0.05
DUCK_PERCENTILE = 60

_FRAME = 8192
_HOP = _FRAME // 2
_SILENT_DB = -120.0
_CHUNK = 256  # frames per FFT batch, keeps memory bounded


def movavg(x: np.ndarray, w: int, dtype=np.float64) -> np.ndarray:
    """Centered moving average in O(N) via a cumulative sum; same length as x.

    Edges average over the part of the window that exists, divided by w. The
    cumulative sum stays float64; the result is written block by block into
    `dtype`, so a float32 result costs half of float64.
    """
    n = len(x)
    c = np.empty(n + 1)
    c[0] = 0.0
    np.cumsum(x, dtype=np.float64, out=c[1:])
    h = w // 2
    hi = w - h  # window reaches hi samples ahead
    m = max(0, min(n, n - hi + 1))  # k + hi <= n
    scale = 1.0 / max(w, 1)
    out = np.empty(n, dtype=dtype)
    for a in range(0, n, _FX_CHUNK):
        b = min(a + _FX_CHUNK, n)
        ahead = np.where(
            np.arange(a, b) < m, c[np.minimum(np.arange(a, b) + hi, n)], c[n]
        )
        behind = c[np.maximum(np.arange(a, b) - h, 0)]
        out[a:b] = (ahead - behind) * scale
    return out


def active_mask(x: np.ndarray, rate: int) -> np.ndarray:
    """True where the signal is 'sung': loud samples dominate a 400 ms window."""
    loud = np.abs(x) > 10 ** (ACTIVE_FLOOR_DBFS / 20)
    win = max(1, int(ACTIVE_WIN_S * rate))
    return movavg(loud, win, np.float32) > ACTIVE_FRACTION


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
    x = np.asarray(x)
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
    # Overlap-add keeps the FFT blocks small on a 4-minute song.
    return signal.oaconvolve(
        np.asarray(x, dtype=np.float32), taps.astype(np.float32), "same"
    )


def _hpf2(x: np.ndarray, rate: int, hz: float) -> np.ndarray:
    board = Pedalboard([HighpassFilter(hz), HighpassFilter(hz)])
    return board(np.asarray(x, dtype=np.float32), rate)


def gate(x: np.ndarray, rate: int) -> np.ndarray:
    """High-pass at 110 Hz (twice), then a noise gate that closes the gaps."""
    y = _hpf2(x, rate, HPF_HZ)
    return Pedalboard([NoiseGate(**GATE)])(y, rate)


@dataclass
class VocalOut:
    stereo: np.ndarray  # (2, N) float32, with reverb and slap
    dry: np.ndarray  # (N,) processed mono before the room
    curve_db: np.ndarray  # (27,) match EQ curve that was applied


_FX_CHUNK = 1 << 20  # samples per streamed reverb/slap block


def _room(dry: np.ndarray, rate: int) -> np.ndarray:
    """Dry mono -> (2, N) with reverb and slap added, streamed in blocks.

    Pedalboard keeps its state between calls with reset=False, so the blocks
    join seamlessly while only one block of effect output exists at a time.
    """
    verb = Pedalboard(
        [
            HighpassFilter(250),
            LowpassFilter(7000),
            Reverb(
                room_size=0.42, damping=0.55, wet_level=1.0, dry_level=0.0, width=1.0
            ),
        ]
    )
    slap = Pedalboard(
        [
            HighpassFilter(300),
            LowpassFilter(5000),
            Delay(delay_seconds=SLAP_DELAY_S, feedback=0.12, mix=1.0),
        ]
    )
    verb_gain = np.float32(10 ** (REVERB_DB / 20))
    slap_gain = np.float32(10 ** (SLAP_DB / 20))
    out = np.empty((2, len(dry)), dtype=np.float32)
    for a in range(0, len(dry), _FX_CHUNK):
        block = np.stack([dry[a : a + _FX_CHUNK]] * 2)
        wet = verb(block, rate, reset=False)
        wet *= verb_gain
        echo = slap(block, rate, reset=False)
        echo *= slap_gain
        wet += echo
        wet += block
        out[:, a : a + _FX_CHUNK] = wet
    return out


def process_vocal(voc: np.ndarray, rate: int, ref_ltas: np.ndarray) -> VocalOut:
    """Input level, gate, match EQ, compressors, exciter, de-esser, reverb + slap."""
    x = np.asarray(voc, dtype=np.float32).reshape(-1)
    act = active_mask(x, rate)
    sung = bool(act.any())
    if sung:
        rms = np.sqrt(np.mean(x[act].astype(np.float64) ** 2))
        if rms > 0:
            x = (x * (10 ** (INPUT_RMS_DBFS / 20) / rms)).astype(np.float32)
    del act
    g = gate(x, rate)
    del x

    if sung:
        curve = match_curve_db(ltas_db(g, rate), ref_ltas)
        v = apply_curve(g, rate, curve)
    else:
        curve = np.zeros(len(BANDS_HZ))
        v = g
    del g
    v = Pedalboard([Compressor(**COMP_1), Compressor(**COMP_2)])(v, rate)

    # Exciter: new upper harmonics where the phone mic captured none.
    h = _hpf2(v, rate, EXCITER_SPLIT_HZ)
    drive = EXCITER_DRIVE_PEAK / (np.abs(h).max() + 1e-9)
    h *= np.float32(drive)
    np.tanh(h, out=h)
    h /= np.float32(drive)
    sat = _hpf2(h, rate, EXCITER_HPF_HZ)
    del h
    sat *= np.float32(
        np.sqrt(np.mean(v**2) / (np.mean(sat**2) + 1e-12)) * 10 ** (EXCITER_DB / 20)
    )
    v += sat
    del sat

    # De-esser: compress only the band above 6 kHz.
    hi = _hpf2(v, rate, DEESS_HZ)
    v -= hi
    hi = Pedalboard([Compressor(**DEESS)])(hi, rate)
    v += hi
    del hi
    dry = np.asarray(v, dtype=np.float32).reshape(-1)

    return VocalOut(_room(dry, rate), dry, np.asarray(curve, dtype=np.float64))


def _pad(x: np.ndarray, n: int) -> np.ndarray:
    extra = n - x.shape[-1]
    if extra <= 0:
        return x
    return np.pad(x, [(0, 0)] * (x.ndim - 1) + [(0, extra)])


_LUFS_CHUNK = 1 << 20


def _lufs(x2: np.ndarray, rate: int, mask: np.ndarray | None = None) -> float:
    """BS.1770 integrated loudness of a (2, N) signal, optionally only where mask.

    Same K-weighting filters and gating as pyloudnorm.Meter.integrated_loudness,
    but the filter runs in blocks with carried state and only the cumulative
    energy at the gating-block edges is kept, so no full-length copy of the
    signal is made. With a mask the result equals measuring x2[:, mask].
    """
    meter = pyloudnorm.Meter(rate)
    stages = list(meter._filters.values())
    n = int(mask.sum()) if mask is not None else x2.shape[1]
    block = meter.block_size
    if n < block * rate:
        return float("-inf")  # shorter than one gating block
    step = 0.25
    n_blocks = int(np.round((n / rate - block) / (block * step)) + 1)
    j = np.arange(n_blocks)
    lo = (block * (j * step) * rate).astype(np.int64)
    hi = np.minimum((block * (j * step + 1) * rate).astype(np.int64), n)
    edges = np.unique(np.concatenate([lo, hi]))
    energy = np.zeros((2, len(edges)))
    for ch in range(2):
        zi = [signal.lfilter_zi(st.b, st.a) * 0.0 for st in stages]
        done = 0
        base = 0.0
        for a in range(0, x2.shape[1], _LUFS_CHUNK):
            piece = x2[ch, a : a + _LUFS_CHUNK]
            if mask is not None:
                piece = piece[mask[a : a + _LUFS_CHUNK]]
            if not len(piece):
                continue
            y = piece.astype(np.float64)
            for k, st in enumerate(stages):
                y, zi[k] = signal.lfilter(st.b, st.a, y, zi=zi[k])
                y *= st.passband_gain
            csum = np.cumsum(y * y)
            first = np.searchsorted(edges, done + 1)
            last = np.searchsorted(edges, done + len(y), side="right")
            energy[ch, first:last] = base + csum[edges[first:last] - done - 1]
            base += csum[-1]
            done += len(y)
    z = (
        energy[:, np.searchsorted(edges, hi)] - energy[:, np.searchsorted(edges, lo)]
    ) / (block * rate)
    with np.errstate(divide="ignore", invalid="ignore"):
        loud = -0.691 + 10.0 * np.log10(z.sum(axis=0))
        gated = loud >= -70.0
        avg = z[:, gated].mean(axis=1) if gated.any() else np.full(2, np.nan)
        rel = -0.691 + 10.0 * np.log10(avg.sum()) - 10.0
        keep = (loud > rel) & (loud > -70.0)
        avg = z[:, keep].mean(axis=1) if keep.any() else np.full(2, np.nan)
        value = -0.691 + 10.0 * np.log10(np.nan_to_num(avg).sum())
    return float(value) if np.isfinite(value) else float("-inf")


def mix_vocal(
    vocal: VocalOut, inst: np.ndarray, rate: int, inplace: bool = False
) -> tuple[np.ndarray, dict[str, float]]:
    """Carve the instrumental, set the vocal 2 LU ahead, duck under the voice.

    With inplace=True the carved and ducked instrumental is built in `inst`'s own
    buffer when no padding is needed (saves one full-length copy).
    """
    n = max(vocal.stereo.shape[1], inst.shape[1])
    voc = _pad(vocal.stereo, n)
    dry = _pad(vocal.dry, n)
    bell = Pedalboard([PeakFilter(*INST_BELL)])
    if inplace and inst.dtype == np.float32 and inst.shape[1] == n:
        ins = inst
        for a in range(0, n, _FX_CHUNK):
            ins[:, a : a + _FX_CHUNK] = bell(
                np.ascontiguousarray(ins[:, a : a + _FX_CHUNK]), rate, reset=False
            )
    else:
        ins = _pad(bell(np.asarray(inst, dtype=np.float32), rate), n)

    act = active_mask(dry, rate)
    mask = act if act.sum() > rate else None
    lv, li = _lufs(voc, rate, mask), _lufs(ins, rate, mask)
    if np.isfinite(lv) and np.isfinite(li):
        gain_db = li + FRONT_LU - lv
    else:
        gain_db = 0.0
    gain = np.float32(10 ** (gain_db / 20))

    if act.any():
        env = movavg(np.abs(dry), int(DUCK_ENV_S * rate), np.float32)
        env /= np.float32(np.percentile(env[act], DUCK_PERCENTILE) + 1e-9)
        np.clip(env, 0, 1, out=env)
    else:
        env = np.zeros(n, dtype=np.float32)
    del act, mask
    env *= np.float32(-DUCK_MAX_DB / 20)
    duck = np.power(np.float32(10.0), env, out=env)  # env is reused for the gain curve
    for a in range(0, n, _LUFS_CHUNK):
        ins[:, a : a + _LUFS_CHUNK] *= duck[a : a + _LUFS_CHUNK]
        ins[:, a : a + _LUFS_CHUNK] += voc[:, a : a + _LUFS_CHUNK] * gain
    info = {"vocal_lufs": lv, "inst_lufs": li, "vocal_gain_db": float(gain_db)}
    return ins, info


LOUDNESS_TOLERANCE_LU = 0.3
LOUDNESS_ROUNDS = 6
SILENT_LUFS = -70.0


def _limiter_makeup_db(threshold_db: float, rate: int) -> float:
    """Level pedalboard's Limiter adds to material it does not limit (makeup gain).

    The JUCE limiter lifts its output by about (3.75 - threshold) dB and caps it
    at 0 dBFS; measured on quiet noise so the caller can take it back out.
    """
    probe = (np.random.default_rng(0).standard_normal((2, rate)) * 1e-3).astype(
        np.float32
    )
    board = Pedalboard([Limiter(threshold_db=threshold_db, release_ms=100)])
    return float(20 * np.log10(np.std(board(probe, rate)) / np.std(probe)))


def finalize_loudness(
    x: np.ndarray, rate: int, target_lufs: float, ceiling_db: float
) -> tuple[np.ndarray, float, float]:
    """Bring a (2, N) mix to target_lufs with a sample peak limit at ceiling_db.

    The input is kept. Each round limits ``x * g_total * pre`` once, so the
    limiter never works on its own output, measures BS.1770 loudness on the
    result and updates g_total from the error (proportional first, then a
    secant step). A round ends the loop only when the loudness is within
    LOUDNESS_TOLERANCE_LU of the target and the peak is at or under the ceiling.
    Returns (out, lufs, peak_db); silent input comes back unchanged.
    """
    x = np.asarray(x, dtype=np.float32)
    if not x.size or float(np.max(np.abs(x))) == 0.0:
        return x, SILENT_LUFS, float("-inf")
    ceiling = 10 ** (ceiling_db / 20)
    lufs0 = _lufs(x, rate)
    if not np.isfinite(lufs0):
        peak = float(np.max(np.abs(x)))
        return x, SILENT_LUFS, float(20 * np.log10(peak))
    if (
        abs(lufs0 - target_lufs) <= LOUDNESS_TOLERANCE_LU
        and float(np.max(np.abs(x))) <= ceiling
    ):
        return x, lufs0, float(20 * np.log10(np.max(np.abs(x))))

    limiter = Pedalboard([Limiter(threshold_db=ceiling_db, release_ms=100)])
    # The limiter lifts its output by makeup_db and caps it near 0 dBFS. Feed it
    # pre_db less, so it starts limiting at the ceiling, then scale the capped
    # output (0 dBFS) down to the ceiling: unlimited material keeps its level.
    makeup_db = _limiter_makeup_db(ceiling_db, rate)
    pre_db = -(ceiling_db + makeup_db)

    def render(g_db: float) -> tuple[np.ndarray, float, float]:
        gained = x * np.float32(10 ** ((g_db + pre_db) / 20))
        out = limiter(gained, rate)
        del gained
        out *= np.float32(ceiling)
        peak = float(np.max(np.abs(out)))
        if peak > ceiling:  # the limiter's internal clip can still pass the ceiling
            out *= np.float32(ceiling / peak)
            peak = ceiling
        return out, _lufs(out, rate), peak

    g_db = target_lufs - lufs0
    prev: tuple[float, float] | None = None  # (g_db, lufs) of the last round
    best: tuple[float, float] | None = None  # (abs error, g_db)
    out, lufs, peak = None, lufs0, 0.0
    for _ in range(LOUDNESS_ROUNDS):
        out, lufs, peak = render(g_db)
        err = target_lufs - lufs
        if best is None or abs(err) < best[0]:
            best = (abs(err), g_db)
        if abs(err) <= LOUDNESS_TOLERANCE_LU and peak <= ceiling:
            break
        slope = 1.0
        if prev is not None and abs(g_db - prev[0]) > 1e-6:
            slope = (lufs - prev[1]) / (g_db - prev[0])
            slope = float(np.clip(slope, 0.1, 1.5))
        prev = (g_db, lufs)
        g_db += err / slope
        del out
        out = None
    else:
        if best is not None and best[1] != prev[0]:
            out, lufs, peak = render(best[1])
    if out is None:
        out, lufs, peak = render(best[1])
    peak_db = 20 * np.log10(peak) if peak > 0 else float("-inf")
    return out, (lufs if np.isfinite(lufs) else SILENT_LUFS), float(peak_db)
