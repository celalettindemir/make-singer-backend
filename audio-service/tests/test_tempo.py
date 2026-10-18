"""Tempo olcumu testleri: sentetik vurus izi, ag ve ffmpeg yok."""

import numpy as np
import pytest
from pydantic import ValidationError
from pydub import AudioSegment

import services.tempo as tempo_module
from main import TempoRequest
from services.tempo import TempoService, tempo_tahmini

ORNEK_HIZI = 22050


def vurus_izi(bpm, saniye=30.0, vurgulu=True):
    """Her vuruusta kisa bir gurultu patlamasi; olcu basinda daha guclu."""
    rng = np.random.default_rng(7)
    ornekler = np.zeros(int(ORNEK_HIZI * saniye))
    aralik = 60.0 / bpm
    patlama = int(0.02 * ORNEK_HIZI)
    zarf = np.exp(-np.linspace(0, 6, patlama))
    for i, t in enumerate(np.arange(0, saniye - 0.05, aralik)):
        bas = int(t * ORNEK_HIZI)
        guc = 1.0 if (vurgulu and i % 4 == 0) else 0.6
        ornekler[bas : bas + patlama] += guc * zarf * rng.normal(size=patlama)
    return ornekler / np.max(np.abs(ornekler)) * 0.8


@pytest.mark.parametrize("bpm", [72, 89, 100, 120, 128, 150])
def test_tempo_tahmini_vurus_izini_bulur(bpm):
    assert tempo_tahmini(vurus_izi(bpm), ORNEK_HIZI) == pytest.approx(bpm, abs=2)


def test_tempo_tahmini_sessizlikte_none():
    assert tempo_tahmini(np.zeros(ORNEK_HIZI * 10), ORNEK_HIZI) is None


def test_tempo_tahmini_cok_kisa_parcada_none():
    assert tempo_tahmini(vurus_izi(120, saniye=1.0), ORNEK_HIZI) is None


class SahteStorage:
    def __init__(self):
        self.indirilen = []

    def download_key_to_file(self, key, path):
        self.indirilen.append(key)
        with open(path, "wb") as f:
            f.write(b"ses")


@pytest.mark.asyncio
async def test_tempo_servisi_anahtarla_olcer(monkeypatch):
    tam = np.round(vurus_izi(96) * 32767).astype(np.int16)
    ses = AudioSegment(
        tam.tobytes(), frame_rate=ORNEK_HIZI, sample_width=2, channels=1
    )
    monkeypatch.setattr(
        tempo_module.AudioSegment, "from_file", staticmethod(lambda _yol: ses)
    )
    depo = SahteStorage()

    sonuc = await TempoService(depo).olc("stems/u/p/s.mp3")

    assert depo.indirilen == ["stems/u/p/s.mp3"]
    assert sonuc["bpm"] == pytest.approx(96, abs=2)


@pytest.mark.asyncio
async def test_tempo_servisi_depolamasiz_hata():
    with pytest.raises(RuntimeError):
        await TempoService(None).olc("stems/u/p/s.mp3")


def test_tempo_request_fazla_alan_reddedilir():
    assert TempoRequest(input_key="stems/u/p/s.mp3").input_key == "stems/u/p/s.mp3"
    with pytest.raises(ValidationError):
        TempoRequest(input_key="stems/u/p/s.mp3", input_url="https://x")
