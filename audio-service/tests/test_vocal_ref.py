"""Tone reference choice and built-in curves."""

import numpy as np
from scipy import signal

from services.vocal_chain import BANDS_HZ
from services.vocal_curves import BUILTIN_LTAS
from services.vocal_ref import choose_reference, median_midi


def _saw(hz: float, rate: int, seconds: float = 2.0) -> np.ndarray:
    t = np.arange(int(rate * seconds)) / rate
    return (0.3 * signal.sawtooth(2 * np.pi * hz * t)).astype(np.float64)


def _pair(hz: float):
    return _saw(hz, 16000), _saw(hz, 44100)


def test_rehber_yakinsa_rehber_kullanilir():
    t130, _ = _pair(130)
    g140, g140_44 = _pair(140)
    kind, ref = choose_reference(t130, g140, g140_44)
    assert kind == "guide" and ref.shape == (27,)


def test_rehber_7_yarim_tondan_uzaksa_yerlesik():
    t130, _ = _pair(130)
    g300, g300_44 = _pair(300)
    assert choose_reference(t130, g300, g300_44)[0] == "builtin-male"


def test_rehber_yoksa_kadin_egrisi():
    t220, _ = _pair(220)
    kind, ref = choose_reference(t220, None, None)
    assert kind == "builtin-female" and ref.shape == (27,)


def test_sessiz_take_erkek_egrisi():
    assert choose_reference(np.zeros(16000), None, None)[0] == "builtin-male"


def test_sessiz_rehber_yerlesik_egri():
    t130, _ = _pair(130)
    kind, _ = choose_reference(t130, np.zeros(16000), np.zeros(44100))
    assert kind == "builtin-male"


def test_median_midi_sessizde_none():
    assert median_midi(np.zeros(16000)) is None
    assert abs(median_midi(_saw(220, 16000)) - 57) < 0.5


def test_yerlesik_egriler_sonlu_ve_ortalanmis():
    assert set(BUILTIN_LTAS) == {"builtin-male", "builtin-female"}
    for v in BUILTIN_LTAS.values():
        assert v.shape == (27,) and np.all(np.isfinite(v))
        assert abs(np.median(v[(BANDS_HZ >= 500) & (BANDS_HZ <= 1500)])) < 0.01
