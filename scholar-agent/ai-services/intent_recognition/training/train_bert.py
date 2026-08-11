from __future__ import annotations

import argparse
import json
import platform
import time
from pathlib import Path
from typing import Any

import torch
from torch.utils.data import DataLoader, Dataset
from transformers import AutoModelForSequenceClassification, AutoTokenizer, get_linear_schedule_with_warmup

from common import (
    ID2LABEL,
    LABEL2ID,
    classification_metrics,
    percentile,
    read_jsonl,
    seed_everything,
    write_json,
    write_jsonl,
)


class IntentDataset(Dataset[dict[str, Any]]):
    def __init__(self, records: list[dict[str, Any]]) -> None:
        self.records = records

    def __len__(self) -> int:
        return len(self.records)

    def __getitem__(self, index: int) -> dict[str, Any]:
        return self.records[index]


def make_collator(tokenizer: Any, max_length: int):
    def collate(records: list[dict[str, Any]]) -> dict[str, Any]:
        encoded = tokenizer(
            [record["text"] for record in records],
            padding=True,
            truncation=True,
            max_length=max_length,
            return_tensors="pt",
        )
        encoded["labels"] = torch.tensor([LABEL2ID[record["label"]] for record in records], dtype=torch.long)
        encoded["records"] = records
        return encoded

    return collate


def predict(
    model: torch.nn.Module,
    tokenizer: Any,
    records: list[dict[str, Any]],
    device: torch.device,
    max_length: int,
    batch_size: int,
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    loader = DataLoader(IntentDataset(records), batch_size=batch_size, shuffle=False, collate_fn=make_collator(tokenizer, max_length))
    expected: list[str] = []
    predicted: list[str] = []
    outputs: list[dict[str, Any]] = []
    model.eval()
    with torch.no_grad():
        for batch in loader:
            source_records = batch.pop("records")
            batch.pop("labels")
            tensors = {key: value.to(device) for key, value in batch.items()}
            logits = model(**tensors).logits
            probabilities = torch.softmax(logits.float(), dim=-1).cpu()
            guesses = probabilities.argmax(dim=-1).tolist()
            for record, guess, row in zip(source_records, guesses, probabilities.tolist()):
                label = ID2LABEL[int(guess)]
                expected.append(record["label"])
                predicted.append(label)
                outputs.append({
                    "id": record["id"],
                    "text": record["text"],
                    "expected": record["label"],
                    "predicted": label,
                    "probabilities": {ID2LABEL[index]: value for index, value in enumerate(row)},
                })
    return classification_metrics(expected, predicted), outputs


def benchmark_cpu(
    model_path: Path,
    tokenizer: Any,
    records: list[dict[str, Any]],
    max_length: int,
) -> dict[str, float]:
    model = AutoModelForSequenceClassification.from_pretrained(model_path).to("cpu").eval()
    sample_texts = [record["text"] for record in records]
    latencies: list[float] = []
    with torch.no_grad():
        for text in sample_texts[:5]:
            encoded = tokenizer(text, return_tensors="pt", truncation=True, max_length=max_length)
            model(**encoded)
        for text in sample_texts:
            started = time.perf_counter()
            encoded = tokenizer(text, return_tensors="pt", truncation=True, max_length=max_length)
            model(**encoded)
            latencies.append((time.perf_counter() - started) * 1000)
    return {
        "samples": len(latencies),
        "mean_ms": sum(latencies) / len(latencies),
        "p50_ms": percentile(latencies, 0.50),
        "p95_ms": percentile(latencies, 0.95),
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--data-dir", required=True)
    parser.add_argument("--model", default="BAAI/bge-small-zh-v1.5")
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--epochs", type=int, default=8)
    parser.add_argument("--batch-size", type=int, default=32)
    parser.add_argument("--learning-rate", type=float, default=2e-5)
    parser.add_argument("--weight-decay", type=float, default=0.01)
    parser.add_argument("--warmup-ratio", type=float, default=0.1)
    parser.add_argument("--max-length", type=int, default=128)
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()

    seed_everything(args.seed)
    output_dir = Path(args.output_dir)
    best_dir = output_dir / "best_model"
    output_dir.mkdir(parents=True, exist_ok=True)

    data_dir = Path(args.data_dir)
    train_records = read_jsonl(data_dir / "train.jsonl")
    dev_records = read_jsonl(data_dir / "dev.jsonl")
    test_records = read_jsonl(data_dir / "test.jsonl")

    tokenizer = AutoTokenizer.from_pretrained(args.model)
    model = AutoModelForSequenceClassification.from_pretrained(
        args.model,
        num_labels=len(LABEL2ID),
        label2id=LABEL2ID,
        id2label=ID2LABEL,
    )
    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    model.to(device)

    train_loader = DataLoader(
        IntentDataset(train_records),
        batch_size=args.batch_size,
        shuffle=True,
        collate_fn=make_collator(tokenizer, args.max_length),
        generator=torch.Generator().manual_seed(args.seed),
    )
    optimizer = torch.optim.AdamW(model.parameters(), lr=args.learning_rate, weight_decay=args.weight_decay)
    total_steps = len(train_loader) * args.epochs
    scheduler = get_linear_schedule_with_warmup(
        optimizer,
        num_warmup_steps=max(1, int(total_steps * args.warmup_ratio)),
        num_training_steps=total_steps,
    )
    use_amp = device.type == "cuda"
    scaler = torch.amp.GradScaler("cuda", enabled=use_amp)

    history: list[dict[str, Any]] = []
    best_macro_f1 = -1.0
    started = time.perf_counter()
    for epoch in range(1, args.epochs + 1):
        model.train()
        loss_sum = 0.0
        sample_count = 0
        for batch in train_loader:
            batch.pop("records")
            tensors = {key: value.to(device) for key, value in batch.items()}
            optimizer.zero_grad(set_to_none=True)
            with torch.amp.autocast("cuda", dtype=torch.float16, enabled=use_amp):
                loss = model(**tensors).loss
            scaler.scale(loss).backward()
            scaler.step(optimizer)
            scaler.update()
            scheduler.step()
            batch_count = tensors["labels"].shape[0]
            loss_sum += float(loss.detach()) * batch_count
            sample_count += batch_count

        dev_metrics, _ = predict(model, tokenizer, dev_records, device, args.max_length, args.batch_size)
        row = {
            "epoch": epoch,
            "train_loss": loss_sum / sample_count,
            "dev_accuracy": dev_metrics["accuracy"],
            "dev_macro_f1": dev_metrics["macro_f1"],
            "learning_rate": scheduler.get_last_lr()[0],
        }
        history.append(row)
        print(json.dumps(row, ensure_ascii=False), flush=True)
        if dev_metrics["macro_f1"] > best_macro_f1:
            best_macro_f1 = dev_metrics["macro_f1"]
            model.save_pretrained(best_dir, safe_serialization=True)
            tokenizer.save_pretrained(best_dir)

    training_seconds = time.perf_counter() - started
    best_model = AutoModelForSequenceClassification.from_pretrained(best_dir).to(device)
    dev_metrics, dev_predictions = predict(best_model, tokenizer, dev_records, device, args.max_length, args.batch_size)
    test_metrics, test_predictions = predict(best_model, tokenizer, test_records, device, args.max_length, args.batch_size)
    del best_model
    if torch.cuda.is_available():
        torch.cuda.empty_cache()
    cpu_latency = benchmark_cpu(best_dir, tokenizer, test_records, args.max_length)

    parameter_count = sum(parameter.numel() for parameter in model.parameters())
    results = {
        "experiment": "bert_sequence_classification",
        "model": args.model,
        "seed": args.seed,
        "device": str(device),
        "torch_version": torch.__version__,
        "python_version": platform.python_version(),
        "parameters": parameter_count,
        "train_examples": len(train_records),
        "dev_examples": len(dev_records),
        "test_examples": len(test_records),
        "training_seconds": training_seconds,
        "best_dev_macro_f1": best_macro_f1,
        "dev": dev_metrics,
        "test": test_metrics,
        "cpu_latency": cpu_latency,
        "args": vars(args),
    }
    write_json(output_dir / "metrics.json", results)
    write_json(output_dir / "history.json", history)
    write_jsonl(output_dir / "dev_predictions.jsonl", dev_predictions)
    write_jsonl(output_dir / "test_predictions.jsonl", test_predictions)
    print(json.dumps(results, ensure_ascii=False, indent=2), flush=True)


if __name__ == "__main__":
    main()
