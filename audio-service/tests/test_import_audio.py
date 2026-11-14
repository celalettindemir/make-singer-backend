"""Import measurement tests: fake storage, no network."""

import json

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
        import_module.AudioSegment, "from_file", staticmethod(lambda _yol, **_k: ses)
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


class YukleyenDepo(SahteStorage):
    def __init__(self):
        super().__init__()
        self.yuklenen = {}

    def upload_file(self, key, path, content_type):
        with open(path, "rb") as f:
            self.yuklenen[key] = (f.read(), content_type)
        return f"https://r2.test/{key}"


def sinus(hz, saniye, hiz=ORNEK_HIZI, genlik=0.5):
    t = np.arange(int(hiz * saniye)) / hiz
    return genlik * np.sin(2 * np.pi * hz * t)


def tam_sayi(ornekler):
    return np.round(np.clip(ornekler, -1, 1) * 32767).astype(np.int16)


@pytest.mark.asyncio
async def test_hazirla_tum_sureyi_olcer(monkeypatch):
    sahte_ses(monkeypatch, tam_sayi(sinus(220, 45)))
    depo = SahteStorage()
    monkeypatch.setattr(main, "import_service", ImportAudioService(depo))

    yanit = TestClient(main.app).post(
        "/ice-aktar-hazirla", json={"input_key": "imports/u/p/kaynak.mp3"}
    )

    assert yanit.status_code == 200
    assert yanit.json()["duration_ms"] == pytest.approx(45000, abs=50)
    assert depo.indirilen == ["imports/u/p/kaynak.mp3"]


@pytest.mark.asyncio
async def test_hazirla_bozuk_dosya_422(monkeypatch):
    def patla(_yol, **_k):
        raise RuntimeError("bad file")

    monkeypatch.setattr(import_module.AudioSegment, "from_file", staticmethod(patla))
    with pytest.raises(ImportUnreadable):
        await ImportAudioService(SahteStorage()).prepare("imports/u/p/x.mp3")

    monkeypatch.setattr(main, "import_service", ImportAudioService(SahteStorage()))
    yanit = TestClient(main.app).post("/ice-aktar-hazirla", json={"input_key": "k.mp3"})
    assert yanit.status_code == 422
    assert yanit.json()["detail"] == "undecodable"


def vokal(hz):
    return np.concatenate(
        [
            np.zeros(ORNEK_HIZI // 2),
            sinus(hz, 0.6),
            np.zeros(ORNEK_HIZI // 2),
            sinus(hz * 2 ** (2 / 12), 0.8),
            np.zeros(ORNEK_HIZI // 2),
        ]
    )


def kelime(metin, bas, son):
    return {"word": metin, "start_s": bas, "end_s": son, "success": True}


async def notalari_uret(monkeypatch, hz, sections, words):
    sahte_ses(monkeypatch, tam_sayi(vokal(hz)))
    depo = YukleyenDepo()
    sonuc = await ImportAudioService(depo).notes(
        "imports/u/p/vokal.mp3", words, sections, 100.0, "imports/u/p/notes.json"
    )
    return sonuc, depo


@pytest.mark.asyncio
async def test_notalar_kayma_sifir_ve_surum_1(monkeypatch):
    sections = [{"id": "v1", "lines": ["la la", "si si"]}]
    words = [
        kelime("la", 0.5, 1.0),
        kelime("la", 1.0, 1.1),
        kelime("si", 1.6, 2.0),
        kelime("si", 2.0, 2.4),
    ]
    sonuc, depo = await notalari_uret(monkeypatch, 220, sections, words)

    assert sonuc["notes_url"] == "https://r2.test/imports/u/p/notes.json"
    belge = json.loads(depo.yuklenen["imports/u/p/notes.json"][0])
    assert depo.yuklenen["imports/u/p/notes.json"][1] == "application/json"
    assert belge["version"] == 1
    assert belge["offsetMs"] == 0
    assert belge["pitchShiftSemitones"] == 0
    assert len(belge["lines"]) == 2
    assert belge["lines"][0]["sectionId"] == "v1"
    assert sonuc["range_midi"] is not None
    assert sonuc["lines_matched"] >= 1


@pytest.mark.asyncio
async def test_kadin_araligi_secilir(monkeypatch):
    sections = [{"id": "v1", "lines": ["la la"]}]
    words = [kelime("la", 0.5, 1.1), kelime("la", 1.6, 2.4)]
    _sonuc, depo = await notalari_uret(monkeypatch, 440, sections, words)

    belge = json.loads(depo.yuklenen["imports/u/p/notes.json"][0])
    midiler = [
        n["midi"]
        for satir in belge["lines"]
        for s in satir["syllables"]
        for n in s["notes"]
    ]
    assert any(abs(m - 69) <= 1 for m in midiler)


class GercekDosyaStorage:
    """Depo sahtesi: indirilen dosyaya gercek bir wav yazar."""

    def __init__(self, ses):
        self.ses = ses

    def download_key_to_file(self, key, path):
        self.ses.export(path, format="wav")


@pytest.mark.asyncio
async def test_hazirla_uzun_dosya_sinirda_kesilir():
    # 500 s sessizlik, 8 kHz mono: sinirin (482 s) uzerinde.
    uzun = AudioSegment.silent(duration=500_000, frame_rate=8000).set_channels(1)

    sonuc = await ImportAudioService(GercekDosyaStorage(uzun)).prepare(
        "imports/u/p/uzun.wav"
    )

    # Core 480 500 ms ustunu reddeder; kesik okuma yine de bu esigi asar.
    assert 480_500 < sonuc["duration_ms"] <= 482_000
