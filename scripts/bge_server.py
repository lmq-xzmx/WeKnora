#!/usr/bin/env python3
"""
BGE Embedding Server with OpenAI-compatible API format.
Usage: python3 bge_server.py --model /data/bge-m3 --port 8080
"""

import argparse
import numpy as np
from fastapi import FastAPI, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer
import uvicorn
import sys

app = FastAPI(title="BGE Embedding Server")

# Global model instance
model = None
model_name = ""

class EmbeddingRequest(BaseModel):
    model: str
    input: list[str]
    encoding_format: str = "float"
    dimensions: int | None = None

class EmbeddingItem(BaseModel):
    embedding: list[float]
    index: int
    object: str = "embedding"

class EmbeddingResponse(BaseModel):
    object: str = "list"
    data: list[EmbeddingItem]
    model: str
    usage: dict

@app.post("/embeddings")
async def create_embeddings(request: EmbeddingRequest):
    """OpenAI-compatible embeddings endpoint."""
    global model, model_name

    if model is None:
        raise HTTPException(status_code=500, detail="Model not loaded")

    try:
        embeddings = model.encode(request.input, normalize_embeddings=False)

        # Convert to list format
        data = []
        for i, emb in enumerate(embeddings):
            # Convert to Python float list
            embedding_list = [float(x) for x in emb]
            data.append(EmbeddingItem(
                embedding=embedding_list,
                index=i,
                object="embedding"
            ))

        total_tokens = sum(len(inp.split()) for inp in request.input)

        return EmbeddingResponse(
            object="list",
            data=data,
            model=request.model or model_name,
            usage={
                "prompt_tokens": total_tokens,
                "total_tokens": total_tokens
            }
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Embedding error: {str(e)}")

@app.get("/health")
async def health():
    """Health check endpoint."""
    return {"status": "ok", "model": model_name}

@app.get("/v1/models")
async def list_models():
    """List available models."""
    return {
        "object": "list",
        "data": [{
            "id": model_name,
            "object": "model",
            "created": 1234567890,
            "owned_by": "local",
            "embedding_dimensions": model[0].get_sentence_embedding_dimension() if hasattr(model[0], 'get_sentence_embedding_dimension') else 1024
        }]
    }

def load_model(model_path: str):
    """Load the sentence transformer model."""
    global model, model_name
    print(f"Loading model from {model_path}...", file=sys.stderr)
    try:
        model = SentenceTransformer(model_path)
        model_name = model_path.split("/")[-1]
        print(f"Model loaded successfully: {model_name}", file=sys.stderr)
        print(f"Embedding dimension: {model.get_sentence_embedding_dimension()}", file=sys.stderr)
    except Exception as e:
        print(f"Error loading model: {e}", file=sys.stderr)
        sys.exit(1)

def main():
    parser = argparse.ArgumentParser(description="BGE Embedding Server")
    parser.add_argument("--model", type=str, required=True, help="Path to model directory")
    parser.add_argument("--port", type=int, default=8080, help="Port to listen on")
    parser.add_argument("--host", type=str, default="0.0.0.0", help="Host to bind to")
    args = parser.parse_args()

    load_model(args.model)

    print(f"Starting server on {args.host}:{args.port}", file=sys.stderr)
    uvicorn.run(app, host=args.host, port=args.port, log_level="info")

if __name__ == "__main__":
    main()
