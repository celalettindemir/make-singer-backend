FROM python:3.11-slim

WORKDIR /app

# Install ffmpeg and other dependencies
RUN apt-get update && apt-get install -y \
    ffmpeg \
    libsndfile1 \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Copy requirements first for better caching
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY . .

# Run the application
# LOG_LEVEL env var controls uvicorn log verbosity (debug, info, warning, error)
CMD ["sh", "-c", "uvicorn main:app --host 0.0.0.0 --port 8084 --log-level ${LOG_LEVEL:-info}"]
