#!/usr/bin/env python3
"""Frozen, deterministic retrieval evaluator used by the RAG AutoResearch harness."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import re
import time
from pathlib import Path

import networkx as nx
import numpy as np
from rank_bm25 import BM25Okapi
from sklearn.feature_extraction.text import TfidfVectorizer


RUNNER_VERSION = "experiment.evaluation/v1"
TOKEN_RE = re.compile(r"[A-Za-z0-9_]+|[\u3400-\u4dbf\u4e00-\u9fff]")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--corpus", required=True)
    parser.add_argument("--queries", required=True)
    parser.add_argument("--config", required=True)
    parser.add_argument("--cutoff", required=True, type=int)
    return parser.parse_args()


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def read_jsonl(path: Path) -> list[dict]:
    records: list[dict] = []
    with path.open("r", encoding="utf-8") as handle:
        for line_number, line in enumerate(handle, 1):
            line = line.strip()
            if not line:
                continue
            value = json.loads(line)
            if not isinstance(value, dict):
                raise ValueError(f"{path.name}:{line_number} is not an object")
            records.append(value)
    if not records:
        raise ValueError(f"{path.name} is empty")
    return records


def tokenize(text: str) -> list[str]:
    tokens = [token.lower() for token in TOKEN_RE.findall(str(text))]
    cjk = [token for token in tokens if len(token) == 1 and "\u3400" <= token <= "\u9fff"]
    if len(cjk) > 1:
        tokens.extend(left + right for left, right in zip(cjk, cjk[1:]))
    return tokens or ["__empty__"]


def normalized_rank_scores(scores: np.ndarray) -> np.ndarray:
    order = np.argsort(-scores, kind="stable")
    result = np.zeros(len(scores), dtype=float)
    for rank, index in enumerate(order, 1):
        result[index] = 1.0 / rank
    return result


class RetrievalIndex:
    def __init__(self, documents: list[dict], params: dict):
        self.documents = documents
        self.ids = [str(item["id"]) for item in documents]
        self.texts = [str(item["text"]) for item in documents]
        self.tokens = [tokenize(text) for text in self.texts]
        self.params = params
        self._bm25: BM25Okapi | None = None
        self._tfidf: TfidfVectorizer | None = None
        self._tfidf_matrix = None
        self.graph = nx.Graph()
        self.graph.add_nodes_from(self.ids)
        known = set(self.ids)
        for item in documents:
            source = str(item["id"])
            for target in item.get("links", []):
                target = str(target)
                if target in known and target != source:
                    self.graph.add_edge(source, target)

    def bm25_scores(self, query: str) -> np.ndarray:
        if self._bm25 is None:
            self._bm25 = BM25Okapi(
                self.tokens,
                k1=float(self.params.get("k1", 1.5)),
                b=float(self.params.get("b", 0.75)),
            )
        return np.asarray(self._bm25.get_scores(tokenize(query)), dtype=float)

    def tfidf_scores(self, query: str) -> np.ndarray:
        if self._tfidf is None:
            ngram_max = int(self.params.get("ngram_max", 1))
            self._tfidf = TfidfVectorizer(
                tokenizer=tokenize,
                preprocessor=None,
                token_pattern=None,
                lowercase=False,
                ngram_range=(1, max(1, min(2, ngram_max))),
                sublinear_tf=bool(self.params.get("sublinear_tf", True)),
            )
            self._tfidf_matrix = self._tfidf.fit_transform(self.texts)
        query_vector = self._tfidf.transform([query])
        return np.asarray((self._tfidf_matrix @ query_vector.T).toarray()).reshape(-1)

    def hybrid_scores(self, query: str) -> np.ndarray:
        alpha = float(self.params.get("alpha", 0.5))
        rrf_k = max(1.0, float(self.params.get("rrf_k", 60)))
        bm25 = self.bm25_scores(query)
        tfidf = self.tfidf_scores(query)
        bm25_order = np.argsort(-bm25, kind="stable")
        tfidf_order = np.argsort(-tfidf, kind="stable")
        bm25_rank = np.empty(len(bm25_order), dtype=int)
        tfidf_rank = np.empty(len(tfidf_order), dtype=int)
        bm25_rank[bm25_order] = np.arange(1, len(bm25_order) + 1)
        tfidf_rank[tfidf_order] = np.arange(1, len(tfidf_order) + 1)
        return alpha / (rrf_k + bm25_rank) + (1.0 - alpha) / (rrf_k + tfidf_rank)

    def graph_scores(self, query: str) -> np.ndarray:
        base = self.hybrid_scores(query)
        graph_weight = float(self.params.get("graph_weight", 0.2))
        graph_depth = max(1, min(2, int(self.params.get("graph_depth", 1))))
        seed_count = min(len(base), max(5, int(self.params.get("graph_seeds", 10))))
        base_rank = normalized_rank_scores(base)
        graph_boost = np.zeros(len(base), dtype=float)
        id_to_index = {doc_id: index for index, doc_id in enumerate(self.ids)}
        for seed_index in np.argsort(-base, kind="stable")[:seed_count]:
            seed_id = self.ids[int(seed_index)]
            seed_weight = base_rank[int(seed_index)]
            for target, distance in nx.single_source_shortest_path_length(
                self.graph, seed_id, cutoff=graph_depth
            ).items():
                if distance == 0:
                    continue
                graph_boost[id_to_index[target]] += seed_weight / (2**distance)
        if graph_boost.max(initial=0.0) > 0:
            graph_boost /= graph_boost.max()
        return base_rank + graph_weight * graph_boost

    def rank(self, query: str, strategy: str, cutoff: int) -> list[str]:
        if strategy == "bm25":
            scores = self.bm25_scores(query)
        elif strategy == "tfidf":
            scores = self.tfidf_scores(query)
        elif strategy == "hybrid_rrf":
            scores = self.hybrid_scores(query)
        elif strategy == "graph_hybrid":
            if self.graph.number_of_edges() == 0:
                raise ValueError("graph_hybrid requires corpus links")
            scores = self.graph_scores(query)
        else:
            raise ValueError(f"unsupported strategy: {strategy}")
        order = sorted(range(len(scores)), key=lambda index: (-float(scores[index]), self.ids[index]))
        return [self.ids[index] for index in order[:cutoff]]


def query_metrics(relevant: list[str], retrieved: list[str], cutoff: int) -> dict:
    relevant_set = set(relevant)
    hits = [1 if item in relevant_set else 0 for item in retrieved[:cutoff]]
    recall = sum(hits) / len(relevant_set)
    hit_rank = next((index + 1 for index, hit in enumerate(hits) if hit), None)
    reciprocal_rank = 0.0 if hit_rank is None else 1.0 / hit_rank
    dcg = sum(hit / math.log2(index + 2) for index, hit in enumerate(hits))
    ideal_hits = min(len(relevant_set), cutoff)
    idcg = sum(1.0 / math.log2(index + 2) for index in range(ideal_hits))
    ndcg = 0.0 if idcg == 0 else dcg / idcg
    return {
        "hit_rank": hit_rank,
        "recall": recall,
        "reciprocal_rank": reciprocal_rank,
        "ndcg": ndcg,
    }


def main() -> None:
    args = parse_args()
    if args.cutoff < 1 or args.cutoff > 100:
        raise ValueError("cutoff must be in [1, 100]")
    corpus_path = Path(args.corpus)
    queries_path = Path(args.queries)
    config_path = Path(args.config)
    documents = read_jsonl(corpus_path)
    queries = read_jsonl(queries_path)
    config = json.loads(config_path.read_text(encoding="utf-8"))
    strategy = str(config["strategy"])
    params = config.get("parameters", {})
    candidate_id = str(config["id"])
    index = RetrievalIndex(documents, params)

    evidence: list[dict] = []
    totals = {"recall_at_k": 0.0, "mrr": 0.0, "ndcg_at_k": 0.0}
    started = time.perf_counter()
    for query in queries:
        relevant = [str(item) for item in query["relevant_doc_ids"]]
        retrieved = index.rank(str(query["query"]), strategy, args.cutoff)
        metrics = query_metrics(relevant, retrieved, args.cutoff)
        totals["recall_at_k"] += metrics["recall"]
        totals["mrr"] += metrics["reciprocal_rank"]
        totals["ndcg_at_k"] += metrics["ndcg"]
        evidence.append(
            {
                "case_id": str(query["id"]),
                "expected": relevant,
                "observed": retrieved,
                "metrics": {
                    "recall": metrics["recall"],
                    "reciprocal_rank": metrics["reciprocal_rank"],
                    "ndcg": metrics["ndcg"],
                },
                "details": {"hit_rank": metrics["hit_rank"]},
            }
        )
    count = len(queries)
    metrics = {name: value / count for name, value in totals.items()}
    metrics["latency_ms"] = (time.perf_counter() - started) * 1000.0
    result = {
        "version": RUNNER_VERSION,
        "candidate_id": candidate_id,
        "strategy": strategy,
        "parameters": params,
        "metrics": metrics,
        "case_count": count,
        "asset_hashes": {
            "corpus": file_sha256(corpus_path),
            "queries": file_sha256(queries_path),
        },
        "evidence": evidence[:100],
    }
    print(json.dumps(result, ensure_ascii=False, separators=(",", ":")))


if __name__ == "__main__":
    main()
