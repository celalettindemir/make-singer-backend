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

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from services.master import MasterService
from services.encoder import EncoderService
from services.archiver import ArchiverService
from services.storage import StorageService


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
R2_BUCKET_NAME = os.getenv("R2_BUCKET_NAME", "makeasinger")
R2_PUBLIC_URL = os.getenv("R2_PUBLIC_URL", "")


# Request/Response models
class MixChannel(BaseModel):
    stem_url: str
    volume: float = 1.0
    pan: float = 0.0
    mute: bool = False
    solo: bool = False


class VocalTakeInput(BaseModel):
    url: str
    volume: float = 1.0
    pan: float = 0.0


class MasterRequest(BaseModel):
    stem_urls: list[str]
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
    input_url: str
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
    url: str
    filename: str


class ZipRequest(BaseModel):
    files: list[ZipFileEntry]
    output_key: str


class ZipResponse(BaseModel):
    output_url: str
    size: int
    file_count: int


# Services
storage_service: StorageService | None = None
master_service: MasterService | None = None
encoder_service: EncoderService | None = None
archiver_service: ArchiverService | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Initialize services on startup."""
    global storage_service, master_service, encoder_service, archiver_service

    # Initialize storage service
    if R2_ACCESS_KEY_ID and R2_SECRET_ACCESS_KEY:
        storage_service = StorageService(
            account_id=R2_ACCOUNT_ID,
            access_key_id=R2_ACCESS_KEY_ID,
            secret_access_key=R2_SECRET_ACCESS_KEY,
            bucket_name=R2_BUCKET_NAME,
            public_url=R2_PUBLIC_URL,
        )

    # Initialize processing services
    master_service = MasterService(storage_service)
    encoder_service = EncoderService(storage_service)
    archiver_service = ArchiverService(storage_service)

    yield

    # Cleanup on shutdown
    pass


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
            stem_urls=request.stem_urls,
            mix_settings=[s.model_dump() for s in request.mix_settings],
            profile=request.profile,
            vocal_takes=[v.model_dump() for v in request.vocal_takes],
            output_key=request.output_key,
        )
        return MasterResponse(**result)
    except Exception as e:
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
        result = await encoder_service.process(
            input_url=request.input_url,
            format=request.format,
            quality=request.quality,
            sample_rate=request.sample_rate,
            bit_depth=request.bit_depth,
            metadata=request.metadata,
            output_key=request.output_key,
        )
        return EncodeResponse(**result)
    except Exception as e:
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
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8080)
