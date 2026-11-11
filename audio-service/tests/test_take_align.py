import numpy as np

from services.take_align import find_shift_ms, onset_envelope, place_take
from services.vocal_chain import RATE

R16 = 16000


def _song(seed: int, seconds: int) -> np.ndarray:
    rng = np.random.default_rng(seed)
    out = np.zeros(seconds * R16)
    t = int(0.2 * R16)
    while t < len(out) - R16 // 2:
        n = int(0.25 * R16)
        burst = rng.standard_normal(n) * np.exp(-np.arange(n) / (0.05 * R16))
        out[t : t + n] += burst
        t += int(rng.integers(150, 601) * R16 / 1000)
    return out


GUIDE = _song(7, 20)
ENV = onset_envelope(GUIDE, R16)
S0 = 5 * R16


def test_kayma_340ms_bulunur():
    take = np.concatenate([np.zeros(int(0.34 * R16)), GUIDE[S0 : S0 + 8 * R16]])
    shift, corr = find_shift_ms(take, ENV, start_ms=5000)
    assert abs(shift - (-340)) <= 20 and corr >= 0.25


def test_ilgisiz_take_kayma_sifir():
    shift, _ = find_shift_ms(_song(8, 8), ENV, start_ms=5000)
    assert shift == 0


def test_500ms_disina_cikmaz():
    take = np.concatenate([np.zeros(int(0.8 * R16)), GUIDE[S0 : S0 + 8 * R16]])
    shift, _ = find_shift_ms(take, ENV, start_ms=5000)
    assert abs(shift) <= 500


def test_negatif_kaymada_bas_kirpilir_once_tasmaz():
    x = np.arange(RATE, dtype=np.float32)
    at, seg = place_take(x, RATE, start_ms=2000, end_ms=2500, shift_ms=-340)
    assert at == 2 * RATE
    assert seg[0] == x[int(0.34 * RATE)]
    assert len(seg) == RATE // 2


def test_pozitif_kayma_gec_baslatir_end_ms_de_keser():
    x = np.arange(RATE, dtype=np.float32)
    at, seg = place_take(x, RATE, start_ms=2000, end_ms=2500, shift_ms=100)
    assert at == int(2.1 * RATE) and len(seg) == int(0.4 * RATE)
