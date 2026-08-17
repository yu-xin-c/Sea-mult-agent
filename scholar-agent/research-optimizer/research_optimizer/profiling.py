from __future__ import annotations

import hashlib
import json
import math
import re
from datetime import datetime, timezone
from typing import Any

FEATURE_VERSION = "experiment.features/v1"
PROFILE_REQUEST_VERSION = "research-optimizer.profile-request/v1"
TOKEN_PATTERN = re.compile(r"\w+", re.UNICODE)


class ProfileError(ValueError):
    pass


def _canonical_bytes(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=True, sort_keys=True, separators=(",", ":")).encode("utf-8")


def _sha256(value: Any) -> str:
    return hashlib.sha256(_canonical_bytes(value)).hexdigest()


def _dataset_fingerprint(source_files: Any) -> str:
    if not isinstance(source_files, list):
        raise ProfileError("manifest source_files must be a list")
    pairs: list[tuple[str, str]] = []
    for item in source_files:
        if not isinstance(item, dict):
            raise ProfileError("manifest source_files contains an invalid item")
        path = str(item.get("path") or "")
        digest = str(item.get("sha256") or "").lower()
        pairs.append((path, digest))
    pairs.sort()
    payload = "".join(f"{path}\0{digest}\n" for path, digest in pairs).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()


def _mean(values: list[float]) -> float:
    return sum(values) / len(values) if values else 0.0


def _sample_rows(samples: dict[str, Any], name: str) -> list[dict[str, Any]]:
    rows = samples.get(name) or []
    if not isinstance(rows, list) or len(rows) > 256:
        raise ProfileError(f"profile sample {name} must contain at most 256 rows")
    if any(not isinstance(row, dict) for row in rows):
        raise ProfileError(f"profile sample {name} contains a non-object row")
    return rows


def _retrieval_features(samples: dict[str, Any]) -> tuple[dict[str, float], dict[str, bool]]:
    corpus = _sample_rows(samples, "corpus")
    queries = _sample_rows(samples, "search_cases")
    document_chars: list[float] = []
    document_tokens: list[float] = []
    query_chars: list[float] = []
    query_tokens: list[float] = []
    relevance_counts: list[float] = []
    vocabulary: set[str] = set()
    total_tokens = 0
    edge_count = 0
    linked_documents = 0

    for document in corpus:
        text = str(document.get("text") or "")
        tokens = TOKEN_PATTERN.findall(text.lower())
        document_chars.append(float(len(text)))
        document_tokens.append(float(len(tokens)))
        vocabulary.update(tokens)
        total_tokens += len(tokens)
        links = document.get("links") or []
        if isinstance(links, list) and links:
            linked_documents += 1
            edge_count += len({str(item) for item in links if str(item)})

    for query in queries:
        text = str(query.get("query") or "")
        tokens = TOKEN_PATTERN.findall(text.lower())
        query_chars.append(float(len(text)))
        query_tokens.append(float(len(tokens)))
        relevant = query.get("relevant_doc_ids") or []
        relevance_counts.append(float(len(relevant)) if isinstance(relevant, list) else 0.0)

    document_count = len(corpus)
    possible_edges = document_count * max(0, document_count - 1)
    numeric = {
        "document_count": float(document_count),
        "search_case_count": float(len(queries)),
        "profile_sample_documents": float(document_count),
        "profile_sample_queries": float(len(queries)),
        "avg_document_characters": _mean(document_chars),
        "avg_document_tokens": _mean(document_tokens),
        "avg_query_characters": _mean(query_chars),
        "avg_query_tokens": _mean(query_tokens),
        "avg_relevant_documents": _mean(relevance_counts),
        "vocabulary_diversity": len(vocabulary) / max(1, total_tokens),
        "graph_link_ratio": linked_documents / max(1, document_count),
        "graph_density": edge_count / max(1, possible_edges),
    }
    boolean = {"graph_links": edge_count > 0, "labeled_queries": bool(queries and any(relevance_counts))}
    return numeric, boolean


def build_profile(request: dict[str, Any]) -> dict[str, Any]:
    if request.get("version") != PROFILE_REQUEST_VERSION:
        raise ProfileError("unsupported profile request version")
    manifest = request.get("manifest")
    if not isinstance(manifest, dict):
        raise ProfileError("manifest is required")
    samples = request.get("samples") or {}
    if not isinstance(samples, dict):
        raise ProfileError("samples must be an object")
    domain = str(manifest.get("domain") or "")
    adapter = str(manifest.get("adapter") or "")
    if not domain or not adapter:
        raise ProfileError("manifest domain and adapter are required")

    counts = manifest.get("counts") or {}
    capabilities = manifest.get("capabilities") or {}
    numeric = {
        str(key): float(value)
        for key, value in counts.items()
        if isinstance(value, (int, float)) and not isinstance(value, bool)
    }
    boolean = {str(key): bool(value) for key, value in capabilities.items() if isinstance(value, bool)}

    if adapter == "retrieval.v1":
        detailed_numeric, detailed_boolean = _retrieval_features(samples)
        numeric.update(detailed_numeric)
        boolean.update(detailed_boolean)

    if len(numeric) > 128 or len(boolean) > 128 or any(not math.isfinite(value) for value in numeric.values()):
        raise ProfileError("generated feature profile is invalid")
    dataset_fingerprint = _dataset_fingerprint(manifest.get("source_files") or [])
    identity = {
        "domain": domain,
        "adapter": adapter,
        "dataset_fingerprint": dataset_fingerprint,
        "numeric": numeric,
        "boolean": boolean,
    }
    return {
        "version": FEATURE_VERSION,
        "id": f"context-{_sha256(identity)[:16]}",
        "extractor": "python-dataset-profiler/v1",
        "domain": domain,
        "adapter": adapter,
        "dataset_fingerprint": dataset_fingerprint,
        "numeric": numeric,
        "boolean": boolean,
        "created_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    }
