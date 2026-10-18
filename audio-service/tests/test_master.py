"""Mastering zinciri testleri: sentetik ses, ag ve ffmpeg yok."""

import numpy as np
from pydub import AudioSegment

from services.master import PROFILES, MasterService

ORNEK_HIZI = 48000


def sinus(frekans, saniye=1.0, genlik=1.0, kanal=2):
    """Verilen genlikte (0-1) 16 bit sinus; genlik 1.0 tam olcek demek."""
    t = np.arange(int(ORNEK_HIZI * saniye)) / ORNEK_HIZI
    dalga = genlik * np.sin(2 * np.pi * frekans * t)
    tam = np.round(dalga * 32767).astype(np.int16)
    karisik = np.repeat(tam[:, None], kanal, axis=1).reshape(-1)
    return AudioSegment(
        karisik.tobytes(), frame_rate=ORNEK_HIZI, sample_width=2, channels=kanal
    )


def ornekler(ses):
    dizi = np.array(ses.get_array_of_samples(), dtype=np.float64)
    return dizi / float(2 ** (8 * ses.sample_width - 1))


def frekans_genligi(ses, frekans):
    """Sol kanalda tek bir frekansin genligi (DFT izdusumu)."""
    sol = ornekler(ses)[:: ses.channels]
    t = np.arange(len(sol)) / ses.frame_rate
    return 2 * abs(np.mean(sol * np.exp(-2j * np.pi * frekans * t)))


def test_tam_olcek_stemler_toplaninca_kirpilmaz():
    """Iki tam olcek stem ust uste binince tepe kirpilmamali, sekil korunmali."""
    a = sinus(440, genlik=0.9)
    b = sinus(440, genlik=0.9)

    karisim = MasterService()._mix_stems([a, b], [{}, {}])

    s = ornekler(karisim)
    kirpik = np.sum(np.abs(s) >= 0.999)
    assert kirpik == 0
    # Kirpilmis sinus kare dalgaya doner; temiz toplam 3. harmonik tasimaz.
    assert frekans_genligi(karisim, 1320) < 0.01 * frekans_genligi(karisim, 440)


def test_vokal_eklenince_kirpilmaz():
    muzik = sinus(220, genlik=0.9)
    vokal = sinus(220, genlik=0.9, kanal=1)

    toplam = MasterService._sum([muzik, vokal])

    assert np.sum(np.abs(ornekler(toplam)) >= 0.999) == 0
    assert toplam.channels == 2
    assert len(toplam) == len(muzik)


def test_toplam_kisa_parcayi_uzuna_tamamlar():
    uzun = sinus(220, saniye=1.0, genlik=0.2)
    kisa = sinus(220, saniye=0.5, genlik=0.2)

    assert len(MasterService._sum([uzun, kisa])) == len(uzun)


def test_warm_frekans_dengesini_korur():
    """Eski EQ sesi 200 Hz alt geciren ve 8 kHz ust geciren suzgeclerden
    art arda geciriyordu; normalize bunu tek tonda gizler, karisimda gizleyemez."""
    giris = sinus(60, genlik=0.3).overlay(sinus(1000, genlik=0.3))

    cikis = MasterService()._apply_mastering(giris, PROFILES["warm"])

    oran = frekans_genligi(cikis, 1000) / frekans_genligi(cikis, 60)
    # warm basi +2 dB, ortayi duz birakir: oran 1'in biraz altinda kalmali.
    assert 0.5 < oran < 1.0


def test_warm_basi_ortaya_gore_yukseltir():
    bas = sinus(60, genlik=0.3)
    orta = sinus(1000, genlik=0.3)
    servis = MasterService()
    eq = {"eq": PROFILES["warm"]["eq"]}

    bas_kazanc = frekans_genligi(servis._apply_eq(bas, eq), 60) / 0.3
    orta_kazanc = frekans_genligi(servis._apply_eq(orta, eq), 1000) / 0.3

    assert bas_kazanc > orta_kazanc * 1.15


def test_loud_tizi_ortaya_gore_yukseltir():
    tiz = sinus(12000, genlik=0.3)
    orta = sinus(1000, genlik=0.3)
    servis = MasterService()
    eq = {"eq": PROFILES["loud"]["eq"]}

    tiz_kazanc = frekans_genligi(servis._apply_eq(tiz, eq), 12000) / 0.3
    orta_kazanc = frekans_genligi(servis._apply_eq(orta, eq), 1000) / 0.3

    # Tek kutuplu shelf 12 kHz'de nominal +2 dB'nin ~+1 dB'ini verir.
    assert tiz_kazanc > orta_kazanc * 1.1


def test_duz_eq_sesi_degistirmez():
    giris = sinus(1000, genlik=0.3)

    cikis = MasterService()._apply_eq(giris, {"eq": PROFILES["clean"]["eq"]})

    assert np.allclose(ornekler(cikis), ornekler(giris), atol=1e-4)
