from __future__ import annotations

import argparse
import json
import random
import re
from collections import Counter
from pathlib import Path
from typing import Any, Callable

from common import LABELS, file_sha256, read_jsonl, write_json, write_jsonl


FRAMEWORK_PAIRS = [
    ("LangChain", "LlamaIndex"),
    ("LangGraph", "CrewAI"),
    ("AutoGen", "CrewAI"),
    ("Haystack", "LlamaIndex"),
    ("DSPy", "LangChain"),
    ("LangGraph", "AutoGen"),
    ("Semantic Kernel", "LangChain"),
    ("Haystack", "LangChain"),
]
FRAMEWORK_SCENARIOS = ["RAG 问答", "长文档检索", "多 Agent 协作", "工具调用", "知识库问答", "工作流编排"]
FRAMEWORK_METRICS = ["延迟和吞吐", "召回率", "任务成功率", "开发复杂度", "运行成本", "稳定性"]

PAPERS = [
    "Attention Is All You Need",
    "ResNet",
    "BERT",
    "LoRA",
    "GraphSAGE",
    "U-Net",
    "Denoising Diffusion Probabilistic Models",
    "Retrieval-Augmented Generation",
    "Vision Transformer",
    "DeepWalk",
]
PAPER_ACTIONS = [
    "运行官方基线",
    "复核论文主指标",
    "重建训练流程",
    "执行最小 smoke 实验",
    "排查代码和论文结果不一致的问题",
    "运行三组轻量消融",
]

CODE_TASKS = [
    "读取 CSV 并计算均值和标准差",
    "统计日志中每个接口的 P95 延迟",
    "把 JSONL 转换成 CSV",
    "生成不同实验组的箱线图",
    "合并两个结果文件并输出 diff",
    "提取日志中的 request id 并计数",
    "计算 Top-K 准确率",
    "画正弦函数折线图",
    "检查 Python 脚本的时间复杂度并运行测试",
    "清洗缺失值并输出统计摘要",
]

GENERAL_TOPICS = [
    "Transformer 多头注意力",
    "RAG 中的 Query Rewrite",
    "evaluation harness",
    "向量数据库索引",
    "多智能体协作",
    "LoRA 微调",
    "过拟合和早停",
    "混淆矩阵",
    "论文复现中的可重复性",
    "因果推断",
]

TRAIN_TEMPLATES = {
    "Framework_Evaluation": [
        "请比较 {left} 和 {right} 在{scenario}中的{metric}",
        "为 {left} 与 {right} 设计同一数据集上的 benchmark，重点看{metric}",
        "我需要在 {left}、{right} 之间做技术选型，场景是{scenario}",
        "评估 {left} 相比 {right} 在{scenario}里的表现并给出报告",
        "用统一指标测试 {left} 和 {right} 的{metric}",
    ],
    "Paper_Reproduction": [
        "请复现《{paper}》并{action}",
        "按照 {paper} 的实验设置{action}，与论文表格对齐",
        "找到 {paper} 的真实开源实现，然后{action}",
        "我正在重现 {paper}，请帮我{action}",
        "基于论文 {paper} 搭建环境并{action}",
    ],
    "Code_Execution": [
        "请写并运行 Python 脚本，{task}",
        "帮我执行一段代码来{task}",
        "在沙箱中{task}，返回真实输出",
        "生成可运行程序用于{task}",
        "请不要只给代码，还要实际运行并{task}",
        "用 Python 实际完成这个任务：{task}",
        "请执行而不是只解释，目标是{task}",
        "给我一段可复现脚本并运行，要求{task}",
        "启动一个最小代码实验来{task}",
        "请在隔离环境中运行程序以{task}",
        "编写命令行工具来{task}，并展示输出",
        "帮我调试并执行脚本，最终{task}",
        "创建一个小型 Python 程序，实际{task}",
        "需要真实运行结果，请{task}",
        "把下面的数据处理需求写成代码并执行：{task}",
        "运行一次可验证的计算，内容是{task}",
    ],
    "General": [
        "请解释{topic}的基本原理，不需要运行代码",
        "介绍一下{topic}的作用和局限",
        "我想理解{topic}，请用通俗语言说明",
        "总结{topic}的核心概念，但不要做实验",
        "{topic}通常解决什么问题？",
        "请给我一份关于{topic}的概念说明",
        "从入门角度说明{topic}",
        "{topic}有哪些典型应用和限制？",
        "帮我梳理{topic}的知识脉络，不要执行任务",
        "我只想咨询{topic}的原理",
        "用一个简单例子解释{topic}",
        "{topic}和相关概念有什么区别？",
        "请总结{topic}的优点与缺点",
        "怎样理解{topic}这个术语？",
        "给初学者介绍{topic}",
        "说明{topic}的工作机制，不需要生成代码",
    ],
}

DEV_TEMPLATES = {
    "Framework_Evaluation": [
        "在{scenario}场景下，{left} 和 {right} 哪个更适合？请按{metric}实测",
        "做一次 {left} versus {right} 的公平对照实验",
    ],
    "Paper_Reproduction": [
        "重跑论文 {paper} 的实验代码，并{action}",
        "能否把 {paper} 的结果真实跑出来，再{action}",
    ],
    "Code_Execution": [
        "跑个小程序帮我{task}",
        "需要实际计算：{task}",
        "请在 Python 环境完成并验证：{task}",
        "把这个要求实现成可运行代码：{task}",
    ],
    "General": [
        "讲讲{topic}，只做知识解释",
        "什么是{topic}？这里不需要写程序",
        "请回答一个概念问题：{topic}有什么用？",
        "我想学习{topic}，不需要实际运行实验",
    ],
}


def normalize(text: str) -> str:
    return re.sub(r"\s+", "", text).lower()


def candidates(label: str, templates: list[str]) -> list[dict[str, Any]]:
    output: list[dict[str, Any]] = []
    if label == "Framework_Evaluation":
        for template in templates:
            for left, right in FRAMEWORK_PAIRS:
                for scenario in FRAMEWORK_SCENARIOS:
                    for metric in FRAMEWORK_METRICS:
                        text = template.format(left=left, right=right, scenario=scenario, metric=metric)
                        output.append({
                            "text": text,
                            "label": label,
                            "target": {
                                "intent_type": label,
                                "entities": {"frameworks": [left.lower(), right.lower()], "needs_benchmark": True, "topic": scenario},
                                "constraints": {},
                            },
                        })
    elif label == "Paper_Reproduction":
        for template in templates:
            for paper in PAPERS:
                for action in PAPER_ACTIONS:
                    text = template.format(paper=paper, action=action)
                    output.append({
                        "text": text,
                        "label": label,
                        "target": {
                            "intent_type": label,
                            "entities": {
                                "paper_title": paper,
                                "needs_ablation": "消融" in action,
                                "needs_fix": "排查" in action or "不一致" in action,
                            },
                            "constraints": {"reproduction_mode": "smoke"} if "smoke" in action else {},
                        },
                    })
    elif label == "Code_Execution":
        for template in templates:
            for task in CODE_TASKS:
                text = template.format(task=task)
                output.append({
                    "text": text,
                    "label": label,
                    "target": {
                        "intent_type": label,
                        "entities": {"needs_plot": any(term in task for term in ("图", "折线"))},
                        "constraints": {},
                    },
                })
    else:
        for template in templates:
            for topic in GENERAL_TOPICS:
                text = template.format(topic=topic)
                output.append({
                    "text": text,
                    "label": label,
                    "target": {"intent_type": label, "entities": {"topic": topic}, "constraints": {}},
                })
    return output


def build_split(
    split: str,
    templates: dict[str, list[str]],
    per_label: int,
    forbidden: set[str],
    seed: int,
) -> list[dict[str, Any]]:
    rng = random.Random(seed)
    records: list[dict[str, Any]] = []
    seen = set(forbidden)
    for label in LABELS:
        pool = candidates(label, templates[label])
        rng.shuffle(pool)
        selected: list[dict[str, Any]] = []
        for item in pool:
            key = normalize(item["text"])
            if key in seen:
                continue
            seen.add(key)
            selected.append(item)
            if len(selected) == per_label:
                break
        if len(selected) != per_label:
            raise RuntimeError(f"not enough unique {label} records for {split}: {len(selected)}")
        records.extend(selected)
    rng.shuffle(records)
    for index, record in enumerate(records, start=1):
        record["id"] = f"{split}-{index:04d}"
    return records


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--test-source", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--train-per-label", type=int, default=160)
    parser.add_argument("--dev-per-label", type=int, default=40)
    parser.add_argument("--seed", type=int, default=20260722)
    args = parser.parse_args()

    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    raw_test = read_jsonl(args.test_source)
    test_records: list[dict[str, Any]] = []
    test_texts: set[str] = set()
    for index, item in enumerate(raw_test, start=1):
        key = normalize(item["text"])
        if key in test_texts:
            raise ValueError(f"duplicate test text: {item['text']}")
        test_texts.add(key)
        test_records.append({
            "id": f"test-{index:04d}",
            "text": item["text"],
            "label": item["label"],
            "target": {"intent_type": item["label"], "entities": {}, "constraints": {}},
        })

    train = build_split("train", TRAIN_TEMPLATES, args.train_per_label, test_texts, args.seed)
    train_texts = {normalize(item["text"]) for item in train}
    dev = build_split("dev", DEV_TEMPLATES, args.dev_per_label, test_texts | train_texts, args.seed + 1)

    paths = {
        "train": output_dir / "train.jsonl",
        "dev": output_dir / "dev.jsonl",
        "test": output_dir / "test.jsonl",
    }
    write_jsonl(paths["train"], train)
    write_jsonl(paths["dev"], dev)
    write_jsonl(paths["test"], test_records)

    all_sets = {
        "train": {normalize(item["text"]) for item in train},
        "dev": {normalize(item["text"]) for item in dev},
        "test": {normalize(item["text"]) for item in test_records},
    }
    intersections = {
        "train_dev": len(all_sets["train"] & all_sets["dev"]),
        "train_test": len(all_sets["train"] & all_sets["test"]),
        "dev_test": len(all_sets["dev"] & all_sets["test"]),
    }
    if any(intersections.values()):
        raise RuntimeError(f"split leakage detected: {intersections}")

    manifest = {
        "seed": args.seed,
        "source_test": str(Path(args.test_source).resolve()),
        "source_test_sha256": file_sha256(args.test_source),
        "counts": {
            split: {"total": len(records), "labels": dict(sorted(Counter(item["label"] for item in records).items()))}
            for split, records in (("train", train), ("dev", dev), ("test", test_records))
        },
        "split_intersections": intersections,
        "files": {split: {"path": path.name, "sha256": file_sha256(path)} for split, path in paths.items()},
    }
    write_json(output_dir / "manifest.json", manifest)
    print(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
