"""Islem servisleri anahtarla mi calisiyor? Ag ve ffmpeg yok: sahteler kullanilir."""

import os

import pytest
from pydantic import ValidationError

import services.encoder as encoder_module
from main import EncodeRequest, MasterRequest, MixChannel, ZipFileEntry
from services.archiver import ArchiverService
from services.encoder import EncoderService


class SahteStorage:
    def __init__(self):
        self.indirilen_anahtarlar = []
        self.yuklenenler = []

    def download_key_to_file(self, key, path):
        self.indirilen_anahtarlar.append(key)
        with open(path, "wb") as f:
            f.write(b"ses")

    def upload_file(self, key, path, content_type):
        self.yuklenenler.append(key)
        return f"https://makeasinger-private.r2.cloudflarestorage.com/{key}"


class SahteAudio:
    """pydub.AudioSegment yerine gecen sahte: ffmpeg'e hic dokunmaz."""

    frame_rate = 48000

    def set_frame_rate(self, *args, **kwargs):
        return self

    def set_sample_width(self, *args, **kwargs):
        return self


def test_master_request_anahtar_alanlari():
    req = MasterRequest(
        stem_keys=["stems/p/s.wav"],
        mix_settings=[],
        vocal_takes=[{"key": "vocals/p/s/t.wav", "volume": 1.0}],
        output_key="masters/p/m.wav",
    )
    assert req.stem_keys == ["stems/p/s.wav"]
    assert req.vocal_takes[0].key == "vocals/p/s/t.wav"


def test_master_request_eski_alan_reddedilir():
    with pytest.raises(ValidationError):
        MasterRequest(
            stem_urls=["https://x/stems/p/s.wav"],
            mix_settings=[],
            output_key="masters/p/m.wav",
        )


def test_mix_channel_stem_url_reddedilir():
    """Imzali URL istek govdesine girmesin: alan iki taraftan da kalkti."""
    with pytest.raises(ValidationError):
        MixChannel(
            stem_url="https://x.r2.cloudflarestorage.com/stems/a.wav?X-Amz-Signature=g",
            volume=1.0,
        )


def test_mix_channel_yalnizca_karisim_alanlari():
    kanal = MixChannel(volume=0.5, pan=0.1, mute=True, solo=False)
    assert kanal.model_dump() == {
        "volume": 0.5,
        "pan": 0.1,
        "mute": True,
        "solo": False,
    }


def test_master_request_mix_settings_urlsiz_kabul():
    req = MasterRequest(
        stem_keys=["stems/p/s.wav"],
        mix_settings=[{"volume": 1.0}],
        output_key="masters/p/m.wav",
    )
    assert req.mix_settings[0].volume == 1.0


@pytest.mark.asyncio
async def test_archiver_storage_yoksa_hata():
    """Depolama yokken sessizce bos ZIP uretip 'basarili' demek yerine hata."""
    servis = ArchiverService(None)
    with pytest.raises(RuntimeError):
        await servis.process(
            files=[{"key": "stems/p/s.wav", "filename": "s.wav"}],
            output_key="exports/a.zip",
        )


@pytest.mark.asyncio
async def test_archiver_eksik_anahtar_hata():
    """key'i olmayan girdi atlanirsa file_count yalan soyluyordu."""
    servis = ArchiverService(SahteStorage())
    with pytest.raises(ValueError):
        await servis.process(
            files=[{"filename": "s.wav"}],
            output_key="exports/a.zip",
        )


@pytest.mark.asyncio
async def test_archiver_dosya_sayisi_gercek():
    storage = SahteStorage()
    servis = ArchiverService(storage)
    sonuc = await servis.process(
        files=[
            {"key": "stems/p/s1.wav", "filename": "s1.wav"},
            {"key": "stems/p/s2.wav", "filename": "s2.wav"},
        ],
        output_key="exports/a.zip",
    )
    assert sonuc["file_count"] == 2
    assert storage.indirilen_anahtarlar == ["stems/p/s1.wav", "stems/p/s2.wav"]
    assert storage.yuklenenler == ["exports/a.zip"]


def test_encode_request_input_key():
    req = EncodeRequest(
        input_key="masters/p/m.wav", format="mp3", output_key="exports/e.mp3"
    )
    assert req.input_key == "masters/p/m.wav"


def test_zip_file_entry_key():
    e = ZipFileEntry(key="stems/p/s.wav", filename="s.wav")
    assert e.key == "stems/p/s.wav"


@pytest.mark.asyncio
async def test_encoder_anahtarla_indirir(monkeypatch, tmp_path):
    storage = SahteStorage()
    servis = EncoderService(storage)

    # Girdi kod cozme adimi (AudioSegment.from_file) ffmpeg'e gider: sahteye alinir.
    monkeypatch.setattr(
        encoder_module.AudioSegment,
        "from_file",
        staticmethod(lambda path: SahteAudio()),
    )

    # mp3 disari yazma (audio.export) da ffmpeg'e gider: bunu saran gercek
    # yardimci _encode_mp3'u sahteye aliyoruz ki ffmpeg hic cagrilmasin.
    def sahte_encode_mp3(self, audio, tmpdir, quality, metadata):
        output_path = os.path.join(tmpdir, "output.mp3")
        with open(output_path, "wb") as f:
            f.write(b"cikti")
        return output_path, "audio/mpeg"

    monkeypatch.setattr(EncoderService, "_encode_mp3", sahte_encode_mp3)

    await servis.encode(
        input_key="masters/p/m.wav",
        output_key="exports/e.mp3",
        format="mp3",
        quality=320,
        sample_rate=48000,
        bit_depth=24,
        metadata={},
    )

    assert storage.indirilen_anahtarlar == ["masters/p/m.wav"]
    assert storage.yuklenenler == ["exports/e.mp3"]
