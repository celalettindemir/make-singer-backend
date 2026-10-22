"""
Audio Processing Microservice for Make-Singer

Provides endpoints for:
- Mastering (EQ, compression, limiting)
- Audio encoding (MP3/WAV)
- ZIP archiving
"""

import logging
import os
from contextlib import asynccontextmanager
from typing import Literal

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field, model_validator

from services.analiz import AnalizService
from services.archiver import ArchiverService
from services.encoder import EncoderService
from services.guide import GuideService
from services.master import MasterService
from services.pitch import PitchService
from services.storage import StorageService
from services.tempo import TempoService

# Logging configuration
LOG_LEVEL = os.getenv("LOG_LEVEL", "info").upper()
logging.basicConfig(
    level=getattr(logging, LOG_LEVEL, logging.INFO),
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
logger = logging.getLogger("audio-service")

# Configuration from environment
R2_ACCOUNT_ID = os.getenv("R2_ACCOUNT_ID", "")
R2_ACCESS_KEY_ID = os.getenv("R2_ACCESS_KEY_ID", "")
R2_SECRET_ACCESS_KEY = os.getenv("R2_SECRET_ACCESS_KEY", "")
R2_PUBLIC_BUCKET = os.getenv("R2_PUBLIC_BUCKET", "makeasinger-public")
R2_PRIVATE_BUCKET = os.getenv("R2_PRIVATE_BUCKET", "makeasinger-private")
R2_PUBLIC_URL = os.getenv("R2_PUBLIC_URL", "")


# Request/Response models
class MixChannel(BaseModel):
    model_config = {"extra": "forbid"}

    # Kanallar stem_keys ile ayni sirada gelir; adres alani yoktur.
    # stem_url kaldirildi: imzali URL istek govdesine ve loglara dusmesin.
    volume: float = 1.0
    pan: float = 0.0
    mute: bool = False
    solo: bool = False


class VocalTakeInput(BaseModel):
    key: str
    volume: float = 1.0
    pan: float = 0.0
    # Place on the song timeline; missing start_ms means 0 (old behaviour).
    start_ms: int | None = Field(default=None, ge=0)
    end_ms: int | None = Field(default=None, ge=0)
    # Level of the take in dB; missing means 0 dB. Same bounds as the mixer.
    gain_db: float | None = Field(default=None, ge=-60, le=12)

    @model_validator(mode="after")
    def _end_after_start(self):
        if (
            self.start_ms is not None
            and self.end_ms is not None
            and self.end_ms <= self.start_ms
        ):
            raise ValueError("end_ms must be greater than start_ms")
        return self


class MasterRequest(BaseModel):
    model_config = {"extra": "forbid"}

    stem_keys: list[str]
    mix_settings: list[MixChannel]
    profile: str = "clean"  # clean, warm, loud
    vocal_takes: list[VocalTakeInput] = []
    output_key: str


class MasterResponse(BaseModel):
    output_url: str
    duration: float
    peak_db: float
    lufs: float


class EncodeRequest(BaseModel):
    input_key: str
    format: str  # mp3, wav
    quality: int = 320  # for mp3
    sample_rate: int = 48000
    bit_depth: int = 24  # for wav
    metadata: dict[str, str] = {}
    output_key: str


class EncodeResponse(BaseModel):
    output_url: str
    format: str
    size: int


class ZipFileEntry(BaseModel):
    key: str
    filename: str


class ZipRequest(BaseModel):
    files: list[ZipFileEntry]
    output_key: str


class ZipResponse(BaseModel):
    output_url: str
    size: int
    file_count: int


class AnalizRequest(BaseModel):
    model_config = {"extra": "forbid"}

    # Anahtarlar R2 nesne anahtaridir; imzali adres tasinmaz.
    input_key: str
    preview_key: str
    peak_count: int = Field(default=64, ge=1, le=1024)
    preview_seconds: int = Field(default=30, ge=1, le=120)


class AnalizResponse(BaseModel):
    peaks: list[float]
    duration: float
    preview_url: str


class GuideWord(BaseModel):
    model_config = {"extra": "forbid"}

    word: str
    start_s: float
    end_s: float
    success: bool = True


class GuideSectionInput(BaseModel):
    model_config = {"extra": "forbid"}

    id: str
    type: str
    lines: list[str] = []


class GuideAnalyzeRequest(BaseModel):
    model_config = {"extra": "forbid"}

    # Keys are R2 object keys; no signed URL travels.
    guide_key: str
    reference_key: str
    instrumental_key: str
    words: list[GuideWord]
    sections: list[GuideSectionInput] = Field(min_length=1)
    vocal_gender: Literal["m", "f"]
    bpm: float = Field(gt=0)
    aligned_key: str
    notes_key: str


class GuideAnalyzeResponse(BaseModel):
    aligned_url: str
    notes_url: str
    offset_ms: int
    range_midi: list[int] | None
    note_count: int
    lines_matched: int


class PitchShiftItem(BaseModel):
    model_config = {"extra": "forbid"}

    input_key: str
    output_key: str


class PitchShiftRequest(BaseModel):
    model_config = {"extra": "forbid"}

    items: list[PitchShiftItem] = Field(min_length=1, max_length=16)
    semitones: int = Field(ge=-4, le=4)
    # The guide note strip, moved by the same semitones when given.
    notes_key: str | None = None
    notes_output_key: str | None = None

    @model_validator(mode="after")
    def _not_zero(self):
        if self.semitones == 0:
            raise ValueError("semitones must not be 0")
        return self


class PitchShiftResponse(BaseModel):
    outputs: list[str]
    notes_url: str | None = None


class TempoRequest(BaseModel):
    model_config = {"extra": "forbid"}

    input_key: str


class TempoResponse(BaseModel):
    # Ritim bulunamazsa None; cagiran brief'teki BPM'de kalir.
    bpm: float | None


# Services
storage_service: StorageService | None = None
master_service: MasterService | None = None
encoder_service: EncoderService | None = None
archiver_service: ArchiverService | None = None
analiz_service: AnalizService | None = None
tempo_service: TempoService | None = None
guide_service: GuideService | None = None
pitch_service: PitchService | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Initialize services on startup."""
    global storage_service, master_service, encoder_service, archiver_service
    global analiz_service, tempo_service, guide_service, pitch_service

    # Initialize storage service
    if R2_ACCESS_KEY_ID and R2_SECRET_ACCESS_KEY:
        storage_service = StorageService(
            account_id=R2_ACCOUNT_ID,
            access_key_id=R2_ACCESS_KEY_ID,
            secret_access_key=R2_SECRET_ACCESS_KEY,
            public_bucket=R2_PUBLIC_BUCKET,
            private_bucket=R2_PRIVATE_BUCKET,
            public_url=R2_PUBLIC_URL,
        )

    # Initialize processing services
    master_service = MasterService(storage_service)
    encoder_service = EncoderService(storage_service)
    archiver_service = ArchiverService(storage_service)
    analiz_service = AnalizService(storage_service)
    tempo_service = TempoService(storage_service)
    guide_service = GuideService(storage_service)
    pitch_service = PitchService(storage_service)

    yield

    # Cleanup on shutdown


app = FastAPI(
    title="Audio Processing Service",
    description="Mastering, encoding, and archiving for Make-Singer",
    version="1.0.0",
    lifespan=lifespan,
)


@app.get("/health")
async def health_check():
    """Health check endpoint."""
    return {"status": "healthy", "storage_configured": storage_service is not None}


@app.post("/master", response_model=MasterResponse)
async def master_audio(request: MasterRequest):
    """
    Master audio from stems.

    Applies EQ, compression, and limiting based on the selected profile.
    """
    if master_service is None:
        raise HTTPException(status_code=503, detail="Master service not initialized")

    try:
        result = await master_service.process(
            stem_keys=request.stem_keys,
            mix_settings=[s.model_dump() for s in request.mix_settings],
            profile=request.profile,
            vocal_takes=[v.model_dump() for v in request.vocal_takes],
            output_key=request.output_key,
        )
        return MasterResponse(**result)
    # Genis yakalama BILINCLI: FastAPI ucunda son siginak, her hata 500
    # olarak donsun. BLE001 bu yuzden susturuldu.
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/encode", response_model=EncodeResponse)
async def encode_audio(request: EncodeRequest):
    """
    Encode audio to MP3 or WAV format.

    Supports metadata embedding for MP3 files.
    """
    if encoder_service is None:
        raise HTTPException(status_code=503, detail="Encoder service not initialized")

    try:
        result = await encoder_service.encode(
            input_key=request.input_key,
            format=request.format,
            quality=request.quality,
            sample_rate=request.sample_rate,
            bit_depth=request.bit_depth,
            metadata=request.metadata,
            output_key=request.output_key,
        )
        return EncodeResponse(**result)
    # Genis yakalama BILINCLI: FastAPI ucunda son siginak, her hata 500
    # olarak donsun. BLE001 bu yuzden susturuldu.
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/zip", response_model=ZipResponse)
async def create_zip(request: ZipRequest):
    """
    Create a ZIP archive from multiple files.
    """
    if archiver_service is None:
        raise HTTPException(status_code=503, detail="Archiver service not initialized")

    try:
        result = await archiver_service.process(
            files=[f.model_dump() for f in request.files],
            output_key=request.output_key,
        )
        return ZipResponse(**result)
    # Genis yakalama BILINCLI: FastAPI ucunda son siginak, her hata 500
    # olarak donsun. BLE001 bu yuzden susturuldu.
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/analiz", response_model=AnalizResponse)
async def analiz_audio(request: AnalizRequest):
    """
    Kesfet yayini icin dalga formu (peak_count tepe) ve en yuksek RMS'li
    preview_seconds'lik mp3 onizleme uretir. Onizleme preview_key'e yazilir.
    """
    if analiz_service is None:
        raise HTTPException(status_code=503, detail="Analiz service not initialized")

    try:
        result = await analiz_service.analiz(
            input_key=request.input_key,
            preview_key=request.preview_key,
            peak_count=request.peak_count,
            preview_seconds=request.preview_seconds,
        )
        return AnalizResponse(**result)
    # Genis yakalama BILINCLI: FastAPI ucunda son siginak, her hata 500
    # olarak donsun. BLE001 bu yuzden susturuldu.
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/rehber-analiz", response_model=GuideAnalyzeResponse)
async def guide_analyze(request: GuideAnalyzeRequest):
    """Aligns the guide singer to the instrumental and writes its note strip."""
    if guide_service is None:
        raise HTTPException(status_code=503, detail="Guide service not initialized")

    try:
        result = await guide_service.analyze(
            guide_key=request.guide_key,
            reference_key=request.reference_key,
            instrumental_key=request.instrumental_key,
            words=[w.model_dump() for w in request.words],
            sections=[s.model_dump() for s in request.sections],
            vocal_gender=request.vocal_gender,
            bpm=request.bpm,
            aligned_key=request.aligned_key,
            notes_key=request.notes_key,
        )
        return GuideAnalyzeResponse(**result)
    # Broad catch on purpose, like the other endpoints: every error is a 500.
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/perde-kaydir", response_model=PitchShiftResponse)
async def pitch_shift(request: PitchShiftRequest):
    """Shifts every item by the same semitones; outputs in item order."""
    if pitch_service is None:
        raise HTTPException(status_code=503, detail="Pitch service not initialized")

    try:
        outputs = await pitch_service.shift(
            [i.model_dump() for i in request.items], request.semitones
        )
        notes_url = None
        if request.notes_key and request.notes_output_key:
            notes_url = await pitch_service.shift_notes(
                request.notes_key, request.notes_output_key, request.semitones
            )
        return PitchShiftResponse(outputs=outputs, notes_url=notes_url)
    # Broad catch on purpose, like the other endpoints: every error is a 500.
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/tempo", response_model=TempoResponse)
async def tempo_audio(request: TempoRequest):
    """Render edilen sarkinin gercek BPM'i; ritim bulunamazsa bpm None."""
    if tempo_service is None:
        raise HTTPException(status_code=503, detail="Tempo service not initialized")

    try:
        result = await tempo_service.olc(input_key=request.input_key)
        return TempoResponse(**result)
    # Genis yakalama BILINCLI: FastAPI ucunda son siginak, her hata 500
    # olarak donsun. BLE001 bu yuzden susturuldu.
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=str(e))


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8080)
