#!/usr/bin/env python3
"""
PaddleOCR-VL Layout Parsing API Server for WeKnora

Usage:
    /Users/xzmx/.pyenv/versions/3.12.10/bin/python paddleocr_vl_server.py

WeKnora calls POST {endpoint}/layout-parsing with base64 file,
returns JSON with markdown content.
"""

import base64
import warnings

warnings.filterwarnings("ignore")

from fastapi import FastAPI, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel
import uvicorn

from paddleocr import PaddleOCR

print("Initializing PaddleOCR (first run downloads models, may take a while)...")
ocr = PaddleOCR(lang="ch")
print("PaddleOCR initialized.")


class LayoutParsingRequest(BaseModel):
    file: str  # base64 encoded file content
    filename: str = "document.pdf"


def extract_markdown(result) -> str:
    """Convert PaddleOCR result to markdown text."""
    if not result or not result[0]:
        return ""

    markdown_lines = []
    for block in result[0]:
        if not block:
            continue

        # block = [bbox, (text_content, confidence)] for OCR, or [bbox, layout_info, content] for layout
        if len(block) < 2:
            continue

        content = block[1]
        if isinstance(content, tuple):
            text = str(content[0]) if content[0] else ""
        elif isinstance(content, str):
            text = content
        else:
            text = ""

        if not text or not text.strip():
            continue

        markdown_lines.append(text.strip())

    return "\n\n".join(markdown_lines)


app = FastAPI(title="PaddleOCR-VL Layout Parsing API")


@app.post("/layout-parsing")
async def layout_parsing(request: LayoutParsingRequest):
    """
    WeKnora's paddleocr_vl_endpoint API.

    POST with { "file": "<base64>", "filename": "doc.pdf" }
    Returns { "markdown": "...", "images": [] }
    """
    try:
        file_content = base64.b64decode(request.file)
        result = ocr.ocr(file_content, cls=True)
        markdown = extract_markdown(result)

        return JSONResponse({
            "markdown": markdown,
            "images": [],
        })

    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/health")
async def health():
    return {"status": "ok"}


if __name__ == "__main__":
    print("Starting PaddleOCR-VL server on :8085...")
    uvicorn.run(app, host="0.0.0.0", port=8085, log_level="info")
