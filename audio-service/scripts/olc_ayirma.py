"""Local timing/memory gate for the Demucs separation service.

Usage: python scripts/olc_ayirma.py <file> | --sentetik 240
Prints one JSON line: duration_s, load_s, separate_s, peak_rss_mib, threads.
"""

import argparse
import json
import os
import resource
import sys
import tempfile
import time

import numpy as np
from scipy.io import wavfile

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

from services.separation import SAMPLE_RATE, _coz, ayir, demucs_yukle


def sentetik_uret(sure_s: int, yol: str) -> None:
    """Writes a busy stereo test signal: vibrato melody, bass, drums, pads."""
    rng = np.random.default_rng(7)
    n = sure_s * SAMPLE_RATE
    t = np.arange(n) / SAMPLE_RATE
    notalar = np.array([220.0, 247.0, 262.0, 294.0, 330.0, 294.0, 262.0, 247.0])
    f0 = notalar[(t // 0.5).astype(int) % len(notalar)]
    vib = 1 + 0.01 * np.sin(2 * np.pi * 5.5 * t)
    faz = 2 * np.pi * np.cumsum(f0 * vib) / SAMPLE_RATE
    melodi = 0.3 * (np.sin(faz) + 0.3 * np.sin(2 * faz) + 0.1 * np.sin(3 * faz))
    bas = 0.3 * np.sin(2 * np.pi * 55.0 * (1 + (t // 2 % 2) * 0.5) * t)
    adim = int(SAMPLE_RATE * 0.5)  # 120 BPM: one beat every 0.5 s
    davul = np.zeros(n)
    patlama = rng.standard_normal(adim) * np.exp(
        -np.arange(adim) / (0.04 * SAMPLE_RATE)
    )
    for i in range(0, n - adim, adim):
        davul[i : i + adim] += 0.4 * patlama
    pad = 0.1 * sum(np.sin(2 * np.pi * f * t) for f in (130.8, 164.8, 196.0))
    sol = melodi + bas + davul + pad
    sag = melodi * 0.8 + bas + np.roll(davul, 300) + pad * 1.1
    kar = np.stack([sol, sag], axis=1)
    kar = kar / max(1e-9, np.abs(kar).max()) * 0.8
    wavfile.write(yol, SAMPLE_RATE, (kar * 32767).astype(np.int16))


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("dosya", nargs="?")
    p.add_argument("--sentetik", type=int, metavar="SANIYE")
    args = p.parse_args()
    if not args.dosya and not args.sentetik:
        p.error("give a file or --sentetik SECONDS")

    with tempfile.TemporaryDirectory() as tmp:
        yol = args.dosya
        if args.sentetik:
            yol = os.path.join(tmp, "sentetik.wav")
            sentetik_uret(args.sentetik, yol)
        dalga = _coz(yol)

        t0 = time.perf_counter()
        model = demucs_yukle()
        load_s = time.perf_counter() - t0

        t0 = time.perf_counter()
        ayir(dalga, model)
        separate_s = time.perf_counter() - t0

    # Linux reports KiB (macOS would report bytes).
    peak_mib = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024
    print(
        json.dumps(
            {
                "duration_s": round(dalga.shape[1] / SAMPLE_RATE, 1),
                "load_s": round(load_s, 1),
                "separate_s": round(separate_s, 1),
                "peak_rss_mib": round(peak_mib),
                "threads": int(os.getenv("TORCH_THREADS", "2")),
            }
        )
    )


if __name__ == "__main__":
    main()
