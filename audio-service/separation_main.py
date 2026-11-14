"""
Separation Microservice for Make-Singer (bring-your-own-track import).

POST /ayir splits an uploaded track into vocal/drums/bass/rest with Demucs
htdemucs on CPU. Built as its own image (Dockerfile.separation); the code lives
next to audio-service to share services/storage.py.
"""

import asyncio
import logging
import os
from contextlib import asynccontextmanager
from typing import Literal

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from services.separation import SeparationService, SeparationUnreadable
from services.storage import StorageService

LOG_LEVEL = os.getenv("LOG_LEVEL", "info").upper()
logging.basicConfig(
    level=getattr(logging, LOG_LEVEL, logging.INFO),
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
logger = logging.getLogger("separation-service")

R2_ACCOUNT_ID = os.getenv("R2_ACCOUNT_ID", "")
R2_ACCESS_KEY_ID = os.getenv("R2_ACCESS_KEY_ID", "")
R2_SECRET_ACCESS_KEY = os.getenv("R2_SECRET_ACCESS_KEY", "")
R2_PUBLIC_BUCKET = os.getenv("R2_PUBLIC_BUCKET", "makeasinger-public")
R2_PRIVATE_BUCKET = os.getenv("R2_PRIVATE_BUCKET", "makeasinger-private")
R2_PUBLIC_URL = os.getenv("R2_PUBLIC_URL", "")


class SeparateRequest(BaseModel):
    model_config = {"extra": "forbid"}

    input_key: str
    output_prefix: str


class StemOut(BaseModel):
    instrument: Literal["vocal", "drums", "bass", "rest"]
    key: str


class SeparateResponse(BaseModel):
    duration_ms: int
    stems: list[StemOut]


separation_service: SeparationService | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    global separation_service
    storage = StorageService(
        account_id=R2_ACCOUNT_ID,
        access_key_id=R2_ACCESS_KEY_ID,
        secret_access_key=R2_SECRET_ACCESS_KEY,
        public_bucket=R2_PUBLIC_BUCKET,
        private_bucket=R2_PRIVATE_BUCKET,
        public_url=R2_PUBLIC_URL,
    )
    servis = SeparationService(storage, threads=int(os.getenv("TORCH_THREADS", "2")))
    logger.info("Loading htdemucs model")
    await asyncio.to_thread(servis.load)
    separation_service = servis
    logger.info("Separation service ready")
    yield
    separation_service = None


app = FastAPI(title="Make-Singer Separation Service", lifespan=lifespan)


@app.get("/health")
async def health_check():
    return {"status": "healthy", "model_loaded": separation_service is not None}


@app.post("/ayir", response_model=SeparateResponse)
async def ayir_endpoint(request: SeparateRequest):
    """Downloads input_key, separates it, writes <output_prefix>{stem}.mp3."""
    if separation_service is None:
        raise HTTPException(status_code=503, detail="Model not loaded")
    try:
        result = await separation_service.separate(
            input_key=request.input_key, output_prefix=request.output_prefix
        )
        return SeparateResponse(**result)
    except SeparationUnreadable:
        raise HTTPException(status_code=422, detail="undecodable")
    # Broad catch is intentional: last resort of the endpoint, every failure is 500.
    except Exception as e:
        logger.exception("separation failed")
        raise HTTPException(status_code=500, detail=str(e))
