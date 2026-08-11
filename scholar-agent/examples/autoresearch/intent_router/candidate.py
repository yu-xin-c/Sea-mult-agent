"""Editable intent router used by the bounded AutoResearch example."""

INTENT_TYPES = {
    "AutoResearch",
    "Paper_Reproduction",
    "Custom_Benchmark",
    "Framework_Evaluation",
    "Code_Execution",
    "General",
}


def classify(text: str) -> str:
    """Return one production intent type for a user request.

    The baseline is intentionally conservative. AutoResearch may improve this
    file, while the benchmark and evaluator remain frozen.
    """
    normalized = text.lower().strip()

    if "autoresearch" in normalized or "auto research" in normalized:
        return "AutoResearch"
    if any(token in normalized for token in ("复现", "paper", "arxiv", "论文")):
        return "Paper_Reproduction"
    if "benchmark" in normalized and any(token in normalized for token in ("数据", "dataset", "csv", "jsonl")):
        return "Custom_Benchmark"
    if any(token in normalized for token in ("对比", "比较", "framework", " vs ", "框架")):
        return "Framework_Evaluation"
    if any(token in normalized for token in ("代码", "运行", "python", "执行")):
        return "Code_Execution"
    return "General"
