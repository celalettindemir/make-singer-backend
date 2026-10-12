"""Analiz testleri: sentetik ses, ag ve ffmpeg yok."""

import numpy as np
import pytest
from pydantic import ValidationError
from pydub import AudioSegment

import services.analiz as analiz_module
from main import AnalizRequest
from services.analiz import (
    AnalizService,
    en_yuksek_rms_penceresi,
    mono_ornekler,
    tepe_degerleri,
)


def test_tepe_degerleri_adet_ve_normalizasyon():
    ornekler = np.concatenate([np.full(100, 0.25), np.full(100, -0.5)])
    tepeler = tepe_degerleri(ornekler, 4)
    assert tepeler == [0.5, 0.5, 1.0, 1.0]


def test_tepe_degerleri_her_zaman_istenen_uzunlukta():
    assert len(tepe_degerleri(np.ones(10), 64)) == 64
    assert len(tepe_degerleri(np.random.default_rng(1).normal(size=9999), 64)) == 64


def test_tepe_degerleri_sessiz_ve_bos():
    assert tepe_degerleri(np.zeros(500), 8) == [0.0] * 8
    assert tepe_degerleri(np.array([]), 8) == [0.0] * 8
    assert tepe_degerleri(np.ones(5), 0) == []


def test_tepe_degerleri_aralikta():
    tepeler = tepe_degerleri(np.random.default_rng(2).normal(size=48000), 64)
    assert all(0.0 <= t <= 1.0 for t in tepeler)
    assert max(tepeler) == 1.0


def test_en_yuksek_rms_penceresi_yuksek_bolumu_bulur():
    hiz = 100
    sessiz = np.full(hiz * 40, 0.01)
    gurultulu = np.full(hiz * 30, 0.9)
    ornekler = np.concatenate([sessiz, gurultulu, sessiz])
    bas, bit = en_yuksek_rms_penceresi(ornekler, hiz, 30)
    assert (bas, bit) == (hiz * 40, hiz * 70)


def test_en_yuksek_rms_penceresi_kisa_parca_butun():
    assert en_yuksek_rms_penceresi(np.ones(500), 100, 30) == (0, 500)


def test_mono_ornekler_stereo_ortalama():
    # 16 bit stereo: sol tam, sag sifir -> mono yarim
    sol, sag = 16384, 0
    veri = np.array([sol, sag] * 4, dtype=np.int16).tobytes()
    ses = AudioSegment(data=veri, sample_width=2, frame_rate=8000, channels=2)
    mono = mono_ornekler(ses)
    assert len(mono) == 4
    assert np.allclose(mono, 0.25)


class SahteStorage:
    def __init__(self):
        self.indirilen = []
        self.yuklenen = []

    def download_key_to_file(self, key, path):
        self.indirilen.append(key)

    def upload_file(self, key, path, content_type):
        self.yuklenen.append((key, content_type))
        return f"https://makeasinger-private.r2.cloudflarestorage.com/{key}"


def _sentetik_ses(saniye: int, hiz: int = 1000) -> AudioSegment:
    # Ilk yarisi sessiz, ikinci yarisi gurultulu kare dalga.
    yari = saniye * hiz // 2
    dalga = np.concatenate(
        [np.zeros(yari), np.tile([20000, -20000], yari // 2)]
    ).astype(np.int16)
    return AudioSegment(
        data=dalga.tobytes(), sample_width=2, frame_rate=hiz, channels=1
    )


@pytest.mark.asyncio
async def test_analiz_servisi_onizleme_ve_tepeler(monkeypatch):
    ses = _sentetik_ses(80)
    monkeypatch.setattr(analiz_module.AudioSegment, "from_file", lambda path: ses)
    disa_aktarilan = []

    def sahte_export(self, path, format=None, bitrate=None):
        disa_aktarilan.append((len(self), format, bitrate))

    monkeypatch.setattr(analiz_module.AudioSegment, "export", sahte_export)

    storage = SahteStorage()
    sonuc = await AnalizService(storage).analiz(
        input_key="sosyal/y1/master.wav",
        preview_key="sosyal/y1/onizleme.mp3",
        peak_count=64,
        preview_seconds=30,
    )
    assert storage.indirilen == ["sosyal/y1/master.wav"]
    assert storage.yuklenen == [("sosyal/y1/onizleme.mp3", "audio/mpeg")]
    assert len(sonuc["peaks"]) == 64
    assert sonuc["peaks"][0] == 0.0 and sonuc["peaks"][-1] == 1.0
    assert sonuc["duration"] == 80.0
    # 30 sn'lik pencere sessiz olmayan ikinci yaridan secilir.
    assert disa_aktarilan == [(30000, "mp3", "192k")]
    assert sonuc["preview_url"].endswith("sosyal/y1/onizleme.mp3")


@pytest.mark.asyncio
async def test_analiz_servisi_kisa_parca_butunuyle(monkeypatch):
    ses = _sentetik_ses(10)
    monkeypatch.setattr(analiz_module.AudioSegment, "from_file", lambda path: ses)
    uzunluklar = []
    monkeypatch.setattr(
        analiz_module.AudioSegment,
        "export",
        lambda self, path, format=None, bitrate=None: uzunluklar.append(len(self)),
    )
    await AnalizService(SahteStorage()).analiz("k.wav", "p.mp3", 64, 30)
    assert uzunluklar == [10000]


@pytest.mark.asyncio
async def test_analiz_servisi_depolamasiz_hata():
    with pytest.raises(RuntimeError):
        await AnalizService(None).analiz("k", "p", 64, 30)


def test_analiz_request_varsayilanlar_ve_fazla_alan():
    req = AnalizRequest(
        input_key="sosyal/y1/master.wav", preview_key="sosyal/y1/onizleme.mp3"
    )
    assert req.peak_count == 64 and req.preview_seconds == 30
    with pytest.raises(ValidationError):
        AnalizRequest(input_key="a", preview_key="b", input_url="https://x")


def test_analiz_request_sinirlar():
    with pytest.raises(ValidationError):
        AnalizRequest(input_key="a", preview_key="b", peak_count=0)
    with pytest.raises(ValidationError):
        AnalizRequest(input_key="a", preview_key="b", preview_seconds=0)
