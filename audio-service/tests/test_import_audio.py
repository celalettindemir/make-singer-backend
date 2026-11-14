"""Import measurement tests: fake storage, no network."""

import numpy as np
import pytest
from fastapi.testclient import TestClient
from pydub import AudioSegment

import main
import services.import_audio as import_module
from services.import_audio import ImportAudioService, ImportUnreadable
from tests.test_key import ORNEK_HIZI, akor_dizisi
from tests.test_tempo import SahteStorage, vurus_izi


def tiklamali_c_major():
    uzunluk = ORNEK_HIZI * 30
    akor = akor_dizisi([[60, 64, 67]], saniye=30.0)[:uzunluk] * 0.5
    karisim = akor + vurus_izi(120, saniye=30.0) * 0.5
    return np.round(karisim * 32767).astype(np.int16)


def sahte_ses(monkeypatch, ornekler):
    ses = AudioSegment(
        ornekler.tobytes(), frame_rate=ORNEK_HIZI, sample_width=2, channels=1
    )
    monkeypatch.setattr(
        import_module.AudioSegment, "from_file", staticmethod(lambda _yol: ses)
    )


@pytest.mark.asyncio
async def test_olc_ucu_bpm_ve_ton_doner(monkeypatch):
    sahte_ses(monkeypatch, tiklamali_c_major())
    depo = SahteStorage()
    monkeypatch.setattr(main, "import_service", ImportAudioService(depo))

    yanit = TestClient(main.app).post(
        "/ice-aktar-olc", json={"input_key": "imports/u/p/kaynak.mp3"}
    )

    assert yanit.status_code == 200
    govde = yanit.json()
    assert govde["bpm"] == pytest.approx(120, abs=3)
    assert govde["key"] == {"tonic": "C", "scale": "major"}
    assert depo.indirilen == ["imports/u/p/kaynak.mp3"]


@pytest.mark.asyncio
async def test_cozulemeyen_dosya_422(monkeypatch):
    def patla(_yol):
        raise RuntimeError("bad file")

    monkeypatch.setattr(import_module.AudioSegment, "from_file", staticmethod(patla))
    with pytest.raises(ImportUnreadable):
        await ImportAudioService(SahteStorage()).measure("imports/u/p/x.mp3")

    monkeypatch.setattr(main, "import_service", ImportAudioService(SahteStorage()))
    yanit = TestClient(main.app).post("/ice-aktar-olc", json={"input_key": "k.mp3"})
    assert yanit.status_code == 422


def test_olc_istegi_fazla_alan_reddedilir():
    yanit = TestClient(main.app).post(
        "/ice-aktar-olc", json={"input_key": "k.mp3", "input_url": "x"}
    )
    assert yanit.status_code == 422
