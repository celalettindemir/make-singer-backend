"""Ayirma servisi testleri: sahte model, ag yok, torch/demucs yuklenmez.

`agir` isaretli test gercek htdemucs modelini kullanir ve varsayilan kosuda
atlanir (imajda calistirilir).
"""

import asyncio
import threading
import time

import numpy as np
import pytest
from fastapi.testclient import TestClient
from pydub import AudioSegment

import separation_main
from services.separation import (
    SeparationService,
    ayir,
)

SR = 44100


class SahteModel:
    samplerate = 44100
    audio_channels = 2
    sources = ("drums", "bass", "other", "vocals")


def sahte_uygula(model, girdi, **kwargs):
    """Girdiyi dort esit parcaya boler: (1, C, N) -> (1, 4, C, N)."""
    return np.repeat(girdi[:, None, :, :] / 4.0, 4, axis=1)


class SahteStorage:
    def __init__(self, icerik: bytes = b""):
        self.icerik = icerik
        self.yuklenen = {}

    def download_key_to_file(self, key, path):
        with open(path, "wb") as f:
            f.write(self.icerik)

    def upload_file(self, key, path, content_type):
        with open(path, "rb") as f:
            self.yuklenen[key] = (content_type, f.read())
        return key


def _sinus_wav(tmp_path, saniye: float) -> bytes:
    t = np.arange(int(SR * saniye)) / SR
    dalga = (np.sin(2 * np.pi * 220 * t) * 10000).astype(np.int16)
    ses = AudioSegment(data=dalga.tobytes(), sample_width=2, frame_rate=SR, channels=1)
    yol = tmp_path / "kaynak.wav"
    ses.export(str(yol), format="wav")
    return yol.read_bytes()


def _servis(storage, uygula=sahte_uygula):
    return SeparationService(storage, model_yukle=SahteModel, uygula=uygula)


def test_mono_girdi_stereoya_cevrilir():
    dalga = np.random.default_rng(0).standard_normal((1, 4000)).astype(np.float32)
    sonuc = ayir(dalga, SahteModel(), uygula=sahte_uygula)
    for stem in sonuc.values():
        assert stem.shape == (2, 4000)


def test_normalizasyon_geri_alinir():
    # Zero-mean input like real audio: demucs adds the removed mean back to every
    # stem, so only a (near) zero mean keeps the stems summing to the input.
    dalga = np.random.default_rng(1).standard_normal((2, 4000)).astype(np.float32) * 0.1
    dalga -= dalga.mean()
    sonuc = ayir(dalga, SahteModel(), uygula=sahte_uygula)
    toplam = sum(sonuc.values())
    assert np.allclose(toplam, dalga, atol=1e-4)


def test_stem_adlari_esleme():
    dalga = np.zeros((2, 100), dtype=np.float32)
    sonuc = ayir(dalga, SahteModel(), uygula=sahte_uygula)
    assert set(sonuc) == {"vocal", "drums", "bass", "rest"}


def test_ayir_ucu_anahtarlari_onekin_altina_yazar(tmp_path):
    sahte = SahteStorage(_sinus_wav(tmp_path, 3))
    separation_main.separation_service = _servis(sahte)
    istemci = TestClient(separation_main.app)
    yanit = istemci.post(
        "/ayir",
        json={"input_key": "imports/u/j/source.wav", "output_prefix": "imports/u/j/"},
    )
    assert yanit.status_code == 200
    govde = yanit.json()
    assert abs(govde["duration_ms"] - 3000) <= 50
    assert [s["instrument"] for s in govde["stems"]] == [
        "vocal",
        "drums",
        "bass",
        "rest",
    ]
    assert [s["key"] for s in govde["stems"]] == [
        "imports/u/j/vocal.mp3",
        "imports/u/j/drums.mp3",
        "imports/u/j/bass.mp3",
        "imports/u/j/rest.mp3",
    ]
    assert set(sahte.yuklenen) == {s["key"] for s in govde["stems"]}
    tur, veri = sahte.yuklenen["imports/u/j/vocal.mp3"]
    assert tur == "audio/mpeg"
    yol = tmp_path / "vocal.mp3"
    yol.write_bytes(veri)
    mp3 = AudioSegment.from_file(str(yol), format="mp3")
    assert (mp3.frame_rate, mp3.channels) == (44100, 2)


def test_ayir_ucu_fazla_alani_reddeder():
    separation_main.separation_service = _servis(SahteStorage())
    yanit = TestClient(separation_main.app).post(
        "/ayir", json={"input_key": "a", "output_prefix": "b/", "x": 1}
    )
    assert yanit.status_code == 422


def test_servis_yoksa_503():
    separation_main.separation_service = None
    yanit = TestClient(separation_main.app).post(
        "/ayir", json={"input_key": "a", "output_prefix": "b/"}
    )
    assert yanit.status_code == 503


def test_bozuk_dosya_422():
    sahte = SahteStorage(b"bu bir ses degil")
    separation_main.separation_service = _servis(sahte)
    yanit = TestClient(separation_main.app).post(
        "/ayir", json={"input_key": "imports/u/j/source.mp3", "output_prefix": "p/"}
    )
    assert yanit.status_code == 422
    assert yanit.json()["detail"] == "undecodable"
    assert sahte.yuklenen == {}


@pytest.mark.asyncio
async def test_ayni_anda_tek_is(tmp_path):
    iceride = 0
    en_cok = 0
    kilit = threading.Lock()

    def sayacli_uygula(model, girdi, **kwargs):
        nonlocal iceride, en_cok
        with kilit:
            iceride += 1
            en_cok = max(en_cok, iceride)
        time.sleep(0.2)
        with kilit:
            iceride -= 1
        return sahte_uygula(model, girdi, **kwargs)

    sahte = SahteStorage(_sinus_wav(tmp_path, 1))
    servis = _servis(sahte, uygula=sayacli_uygula)
    await asyncio.gather(
        servis.separate("imports/u/a/source.wav", "imports/u/a/"),
        servis.separate("imports/u/b/source.wav", "imports/u/b/"),
    )
    assert en_cok == 1


@pytest.mark.agir
def test_gercek_model_vokali_ayirir(tmp_path):
    rng = np.random.default_rng(0)
    n = SR * 10
    t = np.arange(n) / SR
    ses = 0.3 * np.sin(2 * np.pi * (220 + 5 * np.sin(2 * np.pi * 5 * t)) * t)
    for vurus in np.arange(0, 10, 0.5):
        i = int(vurus * SR)
        ses[i : i + 2000] += (
            rng.standard_normal(2000) * 0.5 * np.exp(-np.arange(2000) / 400)
        )
    ses += rng.standard_normal(n) * 0.01
    pcm = (np.clip(ses, -1, 1) * 32767).astype(np.int16)
    yol = tmp_path / "ornek.wav"
    AudioSegment(data=pcm.tobytes(), sample_width=2, frame_rate=SR, channels=1).export(
        str(yol), format="wav"
    )

    sahte = SahteStorage(yol.read_bytes())
    servis = SeparationService(sahte)  # varsayilan: gercek htdemucs
    sonuc = asyncio.run(servis.separate("imports/u/j/source.wav", "imports/u/j/"))

    assert abs(sonuc["duration_ms"] - 10000) <= 50
    assert [s["instrument"] for s in sonuc["stems"]] == [
        "vocal",
        "drums",
        "bass",
        "rest",
    ]
    enerjiler = {}
    for s in sonuc["stems"]:
        yol_mp3 = tmp_path / (s["instrument"] + ".mp3")
        yol_mp3.write_bytes(sahte.yuklenen[s["key"]][1])
        parca = AudioSegment.from_file(str(yol_mp3), format="mp3")
        assert abs(len(parca) - 10000) <= 100
        enerjiler[s["instrument"]] = parca.rms
    assert max(enerjiler.values()) > 0
    assert len(set(enerjiler.values())) > 1
