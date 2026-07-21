#!/usr/bin/env python3
"""Render the golden Claim-to-Evidence graph into a deterministic PNG preview."""

from __future__ import annotations

import json
import textwrap
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


ROOT = Path(__file__).resolve().parent
GRAPH_PATH = ROOT / "expected_graph.json"
OUTPUT_PATH = ROOT / "claim-evidence-example.png"
WIDTH, HEIGHT = 1600, 900


def font(size: int, bold: bool = False) -> ImageFont.FreeTypeFont | ImageFont.ImageFont:
    candidates = [
        "/System/Library/Fonts/Supplemental/Arial Bold.ttf" if bold else "/System/Library/Fonts/Supplemental/Arial.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf" if bold else "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
    ]
    for candidate in candidates:
        path = Path(candidate)
        if path.exists():
            return ImageFont.truetype(str(path), size)
    return ImageFont.load_default()


def rounded_box(draw: ImageDraw.ImageDraw, xy: tuple[int, int, int, int], fill: str, outline: str) -> None:
    draw.rounded_rectangle(xy, radius=8, fill=fill, outline=outline, width=2)


def arrow(draw: ImageDraw.ImageDraw, start: tuple[int, int], end: tuple[int, int], color: str) -> None:
    mid_x = (start[0] + end[0]) // 2
    draw.line([start, (mid_x, start[1]), (mid_x, end[1]), end], fill=color, width=4)
    draw.polygon([(end[0], end[1]), (end[0] - 12, end[1] - 7), (end[0] - 12, end[1] + 7)], fill=color)


def wrapped(value: str, width: int) -> str:
    return "\n".join(textwrap.wrap(value, width=width, break_long_words=True, break_on_hyphens=False))


def main() -> None:
    graph = json.loads(GRAPH_PATH.read_text(encoding="utf-8"))
    claim = graph["claims"][0]
    criterion = claim["criteria"][0]
    evidence_by_id = {item["id"]: item for item in graph["evidence"]}
    evidence = [evidence_by_id[item] for item in criterion["evidence_ids"]]

    image = Image.new("RGB", (WIDTH, HEIGHT), "#f8fafc")
    draw = ImageDraw.Draw(image)
    title_font = font(38, bold=True)
    heading_font = font(18, bold=True)
    body_font = font(18)
    small_font = font(14)

    draw.rectangle((0, 0, WIDTH, 118), fill="#ffffff")
    draw.text((58, 34), "Claim-to-Evidence Graph", font=title_font, fill="#0f172a")
    draw.text((58, 84), "Frozen rubric  |  execution-derived evidence  |  deterministic verdict", font=small_font, fill="#64748b")
    draw.text((1260, 42), "1 VERIFIED", font=heading_font, fill="#166534")
    draw.text((1260, 76), "Evidence coverage 100%", font=small_font, fill="#475569")

    lanes = [(60, "PAPER CLAIM"), (520, "GRADABLE CRITERION"), (1050, "RUN EVIDENCE")]
    for x, label in lanes:
        draw.text((x, 156), label, font=heading_font, fill="#475569")
    draw.line((50, 194, 1550, 194), fill="#cbd5e1", width=2)

    claim_box = (60, 300, 430, 550)
    criterion_box = (520, 270, 960, 580)
    evidence_boxes = [(1050, 240, 1500, 410), (1050, 470, 1500, 640)]

    rounded_box(draw, claim_box, "#f0fdf4", "#86efac")
    draw.text((84, 324), claim["claim_id"], font=small_font, fill="#166534")
    draw.text((84, 358), "VERIFIED", font=heading_font, fill="#166534")
    draw.text((84, 404), claim["title"], font=heading_font, fill="#0f172a")
    draw.multiline_text((84, 446), wrapped(claim["statement"], 34), font=body_font, fill="#334155", spacing=7)

    rounded_box(draw, criterion_box, "#f0fdf4", "#86efac")
    draw.text((546, 296), criterion["criterion_id"], font=small_font, fill="#166534")
    draw.text((546, 332), "VERIFIED  |  confidence 98%", font=heading_font, fill="#166534")
    draw.multiline_text((546, 380), wrapped(criterion["description"], 42), font=body_font, fill="#0f172a", spacing=8)
    draw.multiline_text((546, 510), wrapped(criterion["observed_value"], 50), font=small_font, fill="#475569", spacing=5)

    for box, node in zip(evidence_boxes, evidence, strict=True):
        rounded_box(draw, box, "#ecfeff", "#67e8f9")
        draw.text((box[0] + 24, box[1] + 24), node["evidence_type"].upper(), font=small_font, fill="#155e75")
        draw.text((box[0] + 24, box[1] + 62), node["artifact_key"], font=heading_font, fill="#164e63")
        draw.text((box[0] + 24, box[1] + 108), f"SHA-256  {node['sha256'][:16]}...", font=small_font, fill="#0e7490")

    arrow(draw, (claim_box[2], 425), (criterion_box[0], 425), "#94a3b8")
    arrow(draw, (criterion_box[2], 405), (evidence_boxes[0][0], 325), "#166534")
    arrow(draw, (criterion_box[2], 455), (evidence_boxes[1][0], 555), "#166534")

    draw.rectangle((0, 760, WIDTH, HEIGHT), fill="#ffffff")
    draw.text((58, 790), "Verdict reason", font=heading_font, fill="#0f172a")
    draw.multiline_text((58, 830), wrapped(criterion["reason"], 125), font=body_font, fill="#334155", spacing=8)
    image.save(OUTPUT_PATH, format="PNG", optimize=True)
    print(OUTPUT_PATH)


if __name__ == "__main__":
    main()
