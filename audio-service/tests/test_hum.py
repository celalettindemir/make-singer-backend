"""Mirilti hazirlama testleri: sentetik ses, ag yok (ffmpeg gerekir)."""

import shutil

import numpy as np
import pytest
from fastapi.testclient import TestClient
from pydub import AudioSegment

import main
from services.hum import HumService, HumUnreadable


class SahteStorage:
    def __init__(self, icerik: bytes, kopya_yolu=None):
        self.icerik = icerik
        self.kopya_yolu = kopya_yolu
        self.yuklenen = []
        self.yuklenen_dosya = None

    def download_key_to_file(self, key, path):
        with open(path, "wb") as f:
            f.write(self.icerik)

    def upload_file(self, key, path, content_type):
        self.yuklenen.append((key, content_type))
        shutil.copy(path, self.kopya_yolu)
        self.yuklenen_dosya = self.kopya_yolu
        return key


def _wav_baytlari(tmp_path, saniye: int) -> bytes:
    hiz = 44100
    t = np.arange(hiz * saniye) / hiz
    dalga = (np.sin(2 * np.pi * 220 * t) * 10000).astype(np.int16)
    ses = AudioSegment(data=dalga.tobytes(), sample_width=2, frame_rate=hiz, channels=1)
    yol = tmp_path / "kaynak.wav"
    ses.export(str(yol), format="wav")
    return yol.read_bytes()


@pytest.mark.asyncio
async def test_mirilti_suresini_olcer_ve_mp3_yazar(tmp_path):
    sahte = SahteStorage(_wav_baytlari(tmp_path, 6), str(tmp_path / "cikti.mp3"))
    sonuc = await HumService(sahte).prepare("hums/u/p/h-src.wav", "hums/u/p/h.mp3")
    assert abs(sonuc["duration_ms"] - 6000) <= 50
    assert sahte.yuklenen == [("hums/u/p/h.mp3", "audio/mpeg")]
    assert AudioSegment.from_file(sahte.yuklenen_dosya, format="mp3").frame_rate > 0


@pytest.mark.asyncio
async def test_bozuk_dosya_okunamaz():
    sahte = SahteStorage(b"bu bir ses degil")
    with pytest.raises(HumUnreadable):
        await HumService(sahte).prepare("hums/u/p/h-src.wav", "hums/u/p/h.mp3")
    assert sahte.yuklenen == []


def test_mirilti_ucu_okunamazda_422(monkeypatch):
    class Sahte:
        async def prepare(self, input_key, output_key):
            raise HumUnreadable("x")

    monkeypatch.setattr(main, "hum_service", Sahte())
    client = TestClient(main.app)
    resp = client.post("/mirilti-hazirla", json={"input_key": "a", "output_key": "b"})
    assert resp.status_code == 422
