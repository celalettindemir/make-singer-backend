"""Key estimate tests: synthetic sine triads, no network, no ffmpeg."""

import numpy as np

from services.key import ton_tahmini

ORNEK_HIZI = 22050


def _frekans(midi):
    return 440.0 * 2 ** ((midi - 69) / 12)


def akor_dizisi(akorlar, saniye=2.0):
    """Each chord is a list of MIDI notes played as summed sines."""
    t = np.arange(int(ORNEK_HIZI * saniye)) / ORNEK_HIZI
    parcalar = [
        sum(np.sin(2 * np.pi * _frekans(m) * t) for m in akor) / len(akor)
        for akor in akorlar
    ]
    return np.concatenate(parcalar) * 0.8


def test_do_majorde_C_major():
    c, f, g = [60, 64, 67], [65, 69, 72], [67, 71, 74]
    assert ton_tahmini(akor_dizisi([c, f, g, c]), ORNEK_HIZI) == ("C", "major")


def test_la_minorde_A_minor():
    am, dm, e = [57, 60, 64], [62, 65, 69], [64, 68, 71]
    assert ton_tahmini(akor_dizisi([am, dm, e, am]), ORNEK_HIZI) == ("A", "minor")


def test_gurultude_ton_yok():
    rng = np.random.default_rng(3)
    assert ton_tahmini(rng.normal(size=ORNEK_HIZI * 8) * 0.3, ORNEK_HIZI) is None


def test_sessizlikte_ton_yok():
    assert ton_tahmini(np.zeros(ORNEK_HIZI * 4), ORNEK_HIZI) is None
