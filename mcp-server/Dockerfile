# syntax=docker/dockerfile:1.6
# Multi-stage + slim + layer-optimized build.
# Stage 1: install deps into a venv (isolated from runtime image).
# Stage 2: copy venv + source into a clean slim runtime.

FROM python:3.11-slim-bookworm AS builder

WORKDIR /app

# Build deps for any C-extension wheels (e.g. cryptography, pydantic-core).
# Single apt-get invocation + cleanup in the same layer.
RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc python3-dev \
    && rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/*

# Copy only requirements first for better layer caching (source changes don't
# invalidate the pip install layer).
COPY requirements.txt ./

# Build into a venv so the slim runtime doesn't need pip.
RUN python -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/venv/bin/pip install --no-cache-dir -r requirements.txt

# Copy source last.
COPY . .

# Install the package itself into the same venv.
RUN /opt/venv/bin/pip install --no-cache-dir -e .


# Runtime: clean slim image, only the venv + source.
FROM python:3.11-slim-bookworm AS runtime

# Copy the prebuilt venv from builder.
COPY --from=builder /opt/venv /opt/venv
# Copy the source (the editable install registered metadata in the venv).
COPY --from=builder /app /app

WORKDIR /app

ENV PATH=/opt/venv/bin:$PATH \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    MCP_HOST=0.0.0.0 \
    MCP_PORT=8000 \
    WEKNORA_BASE_URL=http://app:8080/api/v1
# Set MCP_SERVER_AUTH_TOKEN at runtime; HTTP transport refuses to start without it.

EXPOSE 8000

CMD ["weknora-mcp-server", "--transport", "http", "--host", "0.0.0.0", "--port", "8000"]