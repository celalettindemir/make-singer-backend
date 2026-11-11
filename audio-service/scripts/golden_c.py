"""Golden comparison and bench for the server vocal chain ("C").

usage: python scripts/golden_c.py SPIKE_V3_DIR OUT_DIR [--bench | --builtin]

Reads the spike recordings by path (they never enter the repo), checks take
alignment and the band balance against the C2 spike render, writes the
end-to-end listening file OUT_DIR/C3-sunucu-gercek.mp3, or with --bench times
the whole job on a 240 s tiled song. With --builtin the job runs without a
guide (what production does until the apps send guideUrl): no alignment, the
built-in curve, and OUT_DIR/C4-sunucu-yerlesik-egri.mp3 is written.
"""

import argparse
import asyncio
import ctypes
import logging
import os
import resource
import subprocess
import sys
import tempfile
import threading
import time

import numpy as np
from pydub import AudioSegment
from scipy import signal

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from services import vocal_chain
from services.master import MasterService, _resample, _to_float
from services.take_align import ALIGN_RATE, find_shift_ms, onset_envelope, place_take
from services.vocal_ref import choose_reference

TAKES = [
    ("raw/20-5857a9d1-verse-0.wav", 80),
    ("raw/21-5857a9d1-chorus-1.wav", 55700),
]
GUIDE = "song/guide.mp3"
STEM = "song/stem.mp3"
C2 = "out/alternatifler/sadece-vokal/C2-sunucu-surumu-vokal.wav"
OUT_NAME = "C3-sunucu-gercek.mp3"
OUT_NAME_BUILTIN = "C4-sunucu-yerlesik-egri.mp3"
BANDS = [
    (100, 300),
    (300, 1000),
    (1000, 3000),
    (3000, 6000),
    (6000, 8000),
    (8000, 11000),
    (11000, 16000),
]
BENCH_S = 240
PERIOD_MS = 102800
SHIFT_EXPECT_MS, SHIFT_TOL_MS, MIN_CORR = -340, 30, 0.25
BAND_TOL_DB = 2.0
CHAIN_MAX_S, RSS_MAX_MB = 20.0, 700


def mono(path: str, rate: int) -> np.ndarray:
    seg = AudioSegment.from_file(path).set_channels(1)
    return _resample(_to_float(seg, np.float32)[:, 0], seg.frame_rate, rate)


def band_db(x: np.ndarray, rate: int) -> np.ndarray:
    """Mean band power (dB) of the loud frames, minus the mean over all bands."""
    n, hop = 8192, 4096
    win = np.hanning(n)
    idx = np.arange(0, len(x) - n, hop)
    rms = np.array([np.sqrt(np.mean(x[i : i + n] ** 2)) for i in idx])
    loud = idx[rms > np.percentile(rms, 60)]
    p = np.mean([np.abs(np.fft.rfft(x[i : i + n] * win)) ** 2 for i in loud], axis=0)
    fr = np.fft.rfftfreq(n, 1 / rate)
    db = np.array(
        [10 * np.log10(p[(fr >= lo) & (fr < hi)].mean() + 1e-20) for lo, hi in BANDS]
    )
    return db - db.mean()


class FakeStorage:
    """Maps storage keys to local files; uploads are copied to a temp dir."""

    def __init__(self, files: dict[str, str], out_dir: str):
        self.files, self.out_dir = files, out_dir
        self.uploaded: dict[str, str] = {}

    def download_key_to_file(self, key: str, path: str) -> None:
        if os.path.lexists(path):
            os.remove(path)
        os.symlink(os.path.abspath(self.files[key]), path)

    def upload_file(self, key: str, path: str, content_type: str) -> str:
        dest = os.path.join(self.out_dir, os.path.basename(key))
        with open(path, "rb") as src, open(dest, "wb") as dst:
            dst.write(src.read())
        self.uploaded[key] = dest
        return dest


def current_rss_mb() -> float:
    """Live memory in MB, read without a subprocess (safe next to threads).

    Linux: resident set. macOS: phys_footprint, which leaves out the freed pages
    that the allocator keeps resident and that make plain RSS overstate a stage.
    """
    try:
        with open("/proc/self/statm") as f:
            return int(f.read().split()[1]) * os.sysconf("SC_PAGE_SIZE") / 1e6
    except OSError:
        pass

    libc = ctypes.CDLL("/usr/lib/libSystem.dylib")
    libc.mach_task_self.restype = ctypes.c_uint32
    info = (ctypes.c_uint64 * 64)()
    count = ctypes.c_uint32(128)
    # TASK_VM_INFO = 22; phys_footprint is the u64 at byte offset 144.
    libc.task_info(libc.mach_task_self(), 22, info, ctypes.byref(count))
    return info[18] / 1e6


class StageRss:
    """Polls the live RSS while a stage runs: (RSS at entry, peak during)."""

    def __init__(self):
        self.entry = 0.0
        self.peak = 0.0
        self._stop = threading.Event()

    def _poll(self):
        while not self._stop.is_set():
            self.peak = max(self.peak, current_rss_mb())
            self._stop.wait(0.05)

    def __enter__(self):
        self.entry = self.peak = current_rss_mb()
        self._thread = threading.Thread(target=self._poll, daemon=True)
        self._thread.start()
        return self

    def __exit__(self, *exc):
        self._stop.set()
        self._thread.join()
        self.peak = max(self.peak, current_rss_mb())

    @property
    def extra(self) -> float:
        return self.peak - self.entry


class _ChainTime(logging.Handler):
    def __init__(self):
        super().__init__()
        self.lines: list[str] = []

    def emit(self, record):
        self.lines.append(record.getMessage())


def run_process(files, takes, stem_key, guide_key, out_dir):
    """Run MasterService end to end; return (result, log lines, process s)."""
    handler = _ChainTime()
    log = logging.getLogger("uvicorn.error")
    log.setLevel(logging.INFO)
    log.addHandler(handler)
    storage = FakeStorage(files, out_dir)
    stage = StageRss()
    original = MasterService._apply_vocal_chain

    def measured(self, *args):
        with stage:
            return original(self, *args)

    MasterService._apply_vocal_chain = measured
    started = time.perf_counter()
    result = asyncio.run(
        MasterService(storage).process(
            [stem_key],
            [{}],
            "clean",
            takes,
            "masters/out.wav",
            guide_key,
        )
    )
    elapsed = time.perf_counter() - started
    MasterService._apply_vocal_chain = original
    log.removeHandler(handler)
    handler.lines.append(
        f"chain stage rss: entry={stage.entry:.0f} MB peak={stage.peak:.0f} MB "
        f"extra={stage.extra:.0f} MB"
    )
    return result, handler.lines, elapsed


def chain_seconds(lines: list[str]) -> float:
    for line in lines:
        if "chain_s=" in line:
            return float(line.rsplit("chain_s=", 1)[1])
    return float("nan")


def chain_extra_mb(lines: list[str]) -> float:
    for line in lines:
        if "extra=" in line and "chain stage" in line:
            return float(line.rsplit("extra=", 1)[1].split()[0])
    return float("nan")


def golden(spike: str, out_dir: str, builtin: bool = False) -> bool:
    rate = vocal_chain.RATE
    p = lambda rel: os.path.join(spike, rel)
    guide16 = mono(p(GUIDE), ALIGN_RATE)
    guide44 = mono(p(GUIDE), rate)
    guide_env = onset_envelope(guide16, ALIGN_RATE)
    inst_len = len(mono(p(STEM), rate))

    takes = [(mono(p(rel), rate), ms) for rel, ms in TAKES]
    ok = True
    shifts = []
    if builtin:  # no guide: production behaviour, no alignment
        shifts = [0] * len(takes)
        guide16 = guide44 = None
    else:
        print("alignment")
    for (rel, ms), (x, _) in zip(TAKES, takes):
        if builtin:
            break
        x16 = signal.resample_poly(x, ALIGN_RATE // 100, rate // 100)
        shift, corr = find_shift_ms(x16, guide_env, ms)
        shifts.append(shift)
        good = abs(shift - SHIFT_EXPECT_MS) <= SHIFT_TOL_MS and corr >= MIN_CORR
        ok &= good
        print(
            f"  {os.path.basename(rel)} start_ms={ms} shift_ms={shift} "
            f"corr={corr:.3f} {'ok' if good else 'FAIL'}"
        )

    joined = np.concatenate(
        [signal.resample_poly(x, ALIGN_RATE // 100, rate // 100) for x, _ in takes]
    ).astype(np.float32)
    kind, ref = choose_reference(joined, guide16, guide44)
    print(f"ref={kind}")
    ok &= kind == ("builtin-male" if builtin else "guide")

    timeline = inst_len
    placed = []
    for (x, ms), shift in zip(takes, shifts):
        start, seg = place_take(x, rate, ms, None, shift)
        placed.append((start, seg))
        timeline = max(timeline, start + len(seg))
    track = np.zeros(timeline, dtype=np.float32)
    for start, seg in placed:
        track[start : start + len(seg)] += seg
    out = vocal_chain.process_vocal(track, rate, ref)
    server = band_db(out.stereo.mean(axis=0), rate)
    c2 = band_db(mono(p(C2), rate), rate)
    print("bands (dB rel. mean)   server     C2    diff")
    for (lo, hi), s, c in zip(BANDS, server, c2):
        good = abs(s - c) <= BAND_TOL_DB
        ok &= good
        print(
            f"  {lo:>5}-{hi:<5} Hz  {s:+7.2f} {c:+7.2f} {s - c:+7.2f} "
            f"{'ok' if good else 'FAIL'}"
        )

    # End to end listening file.
    files = {"stems/stem.mp3": p(STEM), "guides/guide.mp3": p(GUIDE)}
    guide_key = None if builtin else "guides/guide.mp3"
    vtakes = []
    for rel, ms in TAKES:
        key = "vocals/" + os.path.basename(rel)
        files[key] = p(rel)
        vtakes.append({"key": key, "start_ms": ms})
    with tempfile.TemporaryDirectory() as tmp:
        result, lines, _ = run_process(files, vtakes, "stems/stem.mp3", guide_key, tmp)
        wav = os.path.join(tmp, "out.wav")
        mp3 = os.path.join(out_dir, OUT_NAME_BUILTIN if builtin else OUT_NAME)
        subprocess.run(
            ["ffmpeg", "-y", "-loglevel", "error", "-i", wav, "-b:a", "320k", mp3],
            check=True,
        )
    for line in lines:
        print("  log:", line)
    print(f"wrote {mp3} lufs={result['lufs']:.1f} peak_db={result['peak_db']:.2f}")
    print("RESULT:", "PASS" if ok else "FAIL")
    return ok


def tiled(path: str, total_ms: int, dest: str) -> None:
    seg = AudioSegment.from_file(path)
    out = AudioSegment.empty()
    while len(out) < total_ms:
        out += seg
    out[:total_ms].export(dest, format="wav")


def bench(spike: str, out_dir: str) -> bool:
    p = lambda rel: os.path.join(spike, rel)
    rss0 = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    cpu0 = time.process_time()
    scale = 1 if sys.platform == "darwin" else 1024  # macOS bytes, Linux KB
    with tempfile.TemporaryDirectory() as tmp:
        stem, guide = os.path.join(tmp, "stem.wav"), os.path.join(tmp, "guide.wav")
        tiled(p(STEM), BENCH_S * 1000, stem)
        tiled(p(GUIDE), BENCH_S * 1000, guide)
        files = {"stems/stem.wav": stem, "guides/guide.wav": guide}
        vtakes = []
        for rel, ms in TAKES:
            k = 0
            while ms + k * PERIOD_MS < BENCH_S * 1000:
                key = f"vocals/{k}-{os.path.basename(rel)}"
                files[key] = p(rel)
                vtakes.append({"key": key, "start_ms": ms + k * PERIOD_MS})
                k += 1
        base = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
        result, lines, elapsed = run_process(
            files, vtakes, "stems/stem.wav", "guides/guide.wav", tmp
        )
    rss1 = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    for line in lines:
        print("  log:", line)
    chain_s = chain_seconds(lines)
    growth_mb = (rss1 - base) * scale / 1e6
    print(f"takes={len(vtakes)} duration={result['duration']:.1f}s")
    print(
        f"chain_s={chain_s:.1f} process_s={elapsed:.1f} "
        f"cpu_total_s={time.process_time() - cpu0:.1f}"
    )
    print(
        f"rss start={rss0 * scale / 1e6:.0f} MB before_process="
        f"{base * scale / 1e6:.0f} MB peak={rss1 * scale / 1e6:.0f} MB "
        f"growth={growth_mb:.0f} MB"
    )
    extra_mb = chain_extra_mb(lines)
    print(
        f"chain stage memory extra={extra_mb:.0f} MB (limit {RSS_MAX_MB}); "
        f"whole-process ru_maxrss growth={growth_mb:.0f} MB is informational: it "
        "includes the existing stem mix and mastering, and on macOS freed pages "
        "stay resident"
    )
    ok = chain_s <= CHAIN_MAX_S and extra_mb <= RSS_MAX_MB
    print("RESULT:", "PASS" if ok else "FAIL")
    return ok


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("spike_dir")
    ap.add_argument("out_dir")
    mode = ap.add_mutually_exclusive_group()
    mode.add_argument("--bench", action="store_true")
    mode.add_argument("--builtin", action="store_true")
    a = ap.parse_args()
    spike = os.path.expanduser(a.spike_dir)
    out_dir = os.path.expanduser(a.out_dir)
    ok = bench(spike, out_dir) if a.bench else golden(spike, out_dir, builtin=a.builtin)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
