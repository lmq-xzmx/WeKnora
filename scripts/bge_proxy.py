#!/usr/bin/env python3
"""
BGE → OpenAI embedding format proxy.

WeKnora generic provider sends: POST /embeddings with
  {"model": "...", "input": [...], "encoding_format": "float", "dimensions": N}
expects response: {"data": [{"embedding": [...]}]}

BGE server (your deployed one at 36.140.179.34) returns: {"embeddings": [[...]]}

This proxy translates the response format.

Usage:
  python3 bge_proxy.py --bge-url http://36.140.179.34:3041 --port 3042

Then point WeKnora builtin-bge-m3 base_url to http://localhost:3042
"""

import argparse
import warnings
warnings.filterwarnings("ignore")
from fastapi import FastAPI, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel
import uvicorn
from typing import Any

app = FastAPI(title="BGE→OpenAI Embedding Proxy")

class ProxyRequest(BaseModel):
    model: str
    input: list[str]
    encoding_format: str = "float"
    dimensions: int | None = None

@app.post("/embeddings")
async def proxy_embeddings(req: ProxyRequest) -> JSONResponse:
    """Translate OpenAI format request to BGE format and back."""
    import httpx
    async with httpx.AsyncClient(timeout=60.0) as client:
        # Translate to BGE format
        bge_payload = {"inputs": req.input}
        try:
            bge_resp = await client.post(
                f"{args.bge_url.rstrip('/')}/embeddings",
                json=bge_payload,
                headers={"Content-Type": "application/json"}
            )
            bge_resp.raise_for_status()
            bge_data = bge_resp.json()
        except Exception as e:
            raise HTTPException(status_code=502, detail=f"BGE server error: {e}")

        embeddings = bge_data.get("embeddings", [])
        if not embeddings:
            raise HTTPException(status_code=502, detail="BGE returned no embeddings")

        # Translate back to OpenAI format
        data = []
        for i, emb in enumerate(embeddings):
            data.append({"embedding": emb, "index": i})

        return JSONResponse(content={
            "object": "list",
            "data": data,
            "model": req.model,
            "usage": {"prompt_tokens": sum(len(x.split()) for x in req.input), "total_tokens": 0}
        })

@app.get("/health")
async def health():
    return {"status": "ok"}

@app.get("/v1/models")
async def list_models():
    return {"object": "list", "data": [{"id": "bge-m3", "object": "model", "owned_by": "local"}]}

@app.post("/v1/embeddings")
async def proxy_v1_embeddings(req: ProxyRequest):
    return await proxy_embeddings(req)

args = None

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="BGE → OpenAI Embedding Proxy")
    parser.add_argument("--bge-url", required=True, help="BGE server base URL")
    parser.add_argument("--port", type=int, default=3042, help="Proxy listen port")
    parser.add_argument("--host", default="127.0.0.1", help="Proxy bind host")
    args = parser.parse_args()
    print(f"Proxying BGE at {args.bge_url} → OpenAI format on {args.host}:{args.port}")
    uvicorn.run(app, host=args.host, port=args.port, log_level="info")
