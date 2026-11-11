"""Vokal zinciri testleri: sentetik sinyal, ag ve ffmpeg yok."""

import time

import numpy as np

from services.vocal_chain import (
    BANDS_HZ,
    RATE,
    apply_curve,
    ltas_db,
    match_curve_db,
    movavg,
)


def egimli_pembe_gurultu(egim_db_oktav):
    """Belirlenimli (seed 1) pembe gurultu; 1 kHz ustu icin oktav basina egim."""
    rng = np.random.default_rng(1)
    n = RATE * 10
    spek = np.fft.rfft(rng.standard_normal(n))
    f = np.fft.rfftfreq(n, 1 / RATE)
    f[0] = 1.0
    kazanc_db = egim_db_oktav * np.log2(np.maximum(f, 1000.0) / 1000.0)
    spek = spek / np.sqrt(f) * 10 ** (kazanc_db / 20)
    x = np.fft.irfft(spek, n)
    return (x / np.abs(x).max() * 0.5).astype(np.float32)


def test_ton_eslemesi_referansa_yaklasir():
    phone = egimli_pembe_gurultu(-2.0)
    ref = egimli_pembe_gurultu(1.0)
    curve = match_curve_db(ltas_db(phone, RATE), ltas_db(ref, RATE))
    out = apply_curve(phone, RATE, curve)
    assert out.dtype == np.float32 and len(out) == len(phone)
    diff = ltas_db(out, RATE) - ltas_db(ref, RATE)
    band = (BANDS_HZ >= 200) & (BANDS_HZ <= 10000)
    diff -= np.median(diff[(BANDS_HZ >= 500) & (BANDS_HZ <= 1500)])
    assert np.all(np.abs(diff[band]) <= 1.5)


def test_ton_eslemesi_sinirlari_korur():
    ref = np.where(BANDS_HZ > 4000, 30.0, np.where(BANDS_HZ < 300, -30.0, 0.0))
    curve = match_curve_db(np.zeros(27), ref)
    assert curve.max() == 12 and curve.min() == -10
    assert np.all(curve[BANDS_HZ > 12000] == curve[BANDS_HZ <= 12000][-1])


def test_sessiz_giris_ltas_sonlu():
    assert np.all(np.isfinite(ltas_db(np.zeros(RATE), RATE)))
    assert np.all(ltas_db(np.zeros(RATE), RATE) == -120)


def test_movavg_dogrusal_zaman():
    x = np.ones(RATE * 240, dtype=np.float32)
    t = time.process_time()
    movavg(x, int(0.4 * RATE))
    assert time.process_time() - t < 2.0
