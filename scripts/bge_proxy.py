#!/usr/bin/env python3
"""
OpenAI-format embedding proxy that bridges WeKnora's generic interface to
either:
  - BGE/HuggingFace-style endpoint at {bge_url}/embeddings
    (request: {"inputs": [...]} → response: {"embeddings": [[...]]})
  - Ollama-style endpoint at {ollama_url}/api/embeddings
    (request: {"model": "...", "prompt": "..."} per single string
     → response: {"embedding": [...]})

Usage:
  # BGE/HuggingFace backend
  python3 bge_proxy.py --bge-url http://36.140.179.34:3041 --port 3042

  # Ollama backend (auto-detected by --ollama-url)
  python3 bge_proxy.py --ollama-url http://127.0.0.1:11434 --model nomic-embed-text --port 3042

The proxy exposes /v1/models and /v1/embeddings in OpenAI format on the
configured listen port.
"""

import argparse
import warnings
warnings.filterwarnings("ignore")
from fastapi import FastAPI, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel
import uvicorn

app = FastAPI(title="OpenAI Embedding Proxy")


class ProxyRequest(BaseModel):
    model: str
    input: list[str]
    encoding_format: str = "float"
    dimensions: int | None = None


# Globals set in __main__.
args = None


@app.get("/health")
async def health():
    return {"status": "ok", "mode": "ollama" if args.ollama_url else "bge"}


@app.get("/v1/models")
async def list_models():
    if args.ollama_url:
        return {"object": "list", "data": [{"id": args.model, "object": "model", "owned_by": "ollama"}]}
    return {"object": "list", "data": [{"id": args.model, "object": "model", "owned_by": "local"}]}


async def _bge_call(client, texts):
    """Forward to a BGE/HF-style /embeddings endpoint."""
    bge_payload = {"inputs": texts}
    bge_resp = await client.post(
        f"{args.bge_url.rstrip('/')}/embeddings",
        json=bge_payload,
        headers={"Content-Type": "application/json"},
    )
    bge_resp.raise_for_status()
    return bge_resp.json().get("embeddings", [])


async def _ollama_call(client, texts):
    """Forward each text to Ollama /api/embeddings (single-prompt API)."""
    import httpx
    embeddings = []
    for text in texts:
        payload = {"model": args.model, "prompt": text}
        if args.dimensions and args.dimensions > 0:
            payload["dimensions"] = args.dimensions
        resp = await client.post(
            f"{args.ollama_url.rstrip('/')}/api/embeddings",
            json=payload,
            headers={"Content-Type": "application/json"},
        )
        resp.raise_for_status()
        embeddings.append(resp.json().get("embedding", []))
    return embeddings


@app.post("/embeddings")
async def proxy_embeddings(req: ProxyRequest):
    return await _do_proxy(req)


@app.post("/v1/embeddings")
async def proxy_v1_embeddings(req: ProxyRequest):
    return await _do_proxy(req)


async def _do_proxy(req: ProxyRequest):
    import httpx
    timeout = httpx.Timeout(60.0)
    async with httpx.AsyncClient(timeout=timeout) as client:
        try:
            if args.ollama_url:
                embeddings = await _ollama_call(client, req.input)
            else:
                embeddings = await _bge_call(client, req.input)
        except httpx.HTTPStatusError as e:
            raise HTTPException(status_code=502, detail=f"upstream HTTP {e.response.status_code}: {e.response.text[:200]}")
        except Exception as e:
            raise HTTPException(status_code=502, detail=f"{type(e).__name__}: {e}")

    if not embeddings:
        raise HTTPException(status_code=502, detail="upstream returned no embeddings")

    data = []
    for i, emb in enumerate(embeddings):
        data.append({"embedding": emb, "index": i})

    return JSONResponse(content={
        "object": "list",
        "data": data,
        "model": req.model,
        "usage": {
            "prompt_tokens": sum(len(x.split()) for x in req.input),
            "total_tokens": sum(len(x.split()) for x in req.input),
        },
    })


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="OpenAI Embedding Format Proxy")
    parser.add_argument("--bge-url", help="BGE/HF-style backend base URL")
    parser.add_argument("--ollama-url", help="Ollama backend base URL (alternative to --bge-url)")
    parser.add_argument("--model", default="bge-m3", help="Model name to advertise and forward to Ollama")
    parser.add_argument("--dimensions", type=int, default=0, help="Force embedding dimensions for Ollama (0=no override)")
    parser.add_argument("--port", type=int, default=3042, help="Proxy listen port")
    parser.add_argument("--host", default="127.0.0.1", help="Proxy bind host")
    a = parser.parse_args()
    if not a.bge_url and not a.ollama_url:
        parser.error("either --bge-url or --ollama-url is required")
    args = a

    mode = "ollama" if args.ollama_url else "bge"
    backend = args.ollama_url or args.bge_url
    print(f"[{mode}] proxy listening on {args.host}:{args.port} → {backend}")
    uvicorn.run(app, host=args.host, port=args.port, log_level="info")