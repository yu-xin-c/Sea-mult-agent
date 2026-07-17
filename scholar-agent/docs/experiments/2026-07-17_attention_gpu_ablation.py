import json
import math
import statistics
import time

import torch


SEED = 42
BATCH_SIZE = 16
SEQ_LEN = 128
D_MODEL = 256
WARMUP_STEPS = 5
REPETITIONS = 5
STEPS_PER_REPEAT = 20


def attention_forward(x, weights, heads, scaling, residual):
    w_q, w_k, w_v, w_o = weights
    batch, seq_len, d_model = x.shape
    head_dim = d_model // heads

    def split_heads(tensor):
        return tensor.view(batch, seq_len, heads, head_dim).transpose(1, 2)

    q = split_heads(x @ w_q)
    k = split_heads(x @ w_k)
    v = split_heads(x @ w_v)
    scores = q @ k.transpose(-2, -1)
    if scaling:
        scores = scores / math.sqrt(head_dim)
    probabilities = torch.softmax(scores, dim=-1)
    context = probabilities @ v
    context = context.transpose(1, 2).contiguous().view(batch, seq_len, d_model)
    output = context @ w_o
    if residual:
        output = output + x
    entropy = -(probabilities * torch.log2(probabilities.clamp_min(1e-12))).sum(dim=-1).mean()
    return output, entropy


def benchmark_config(x, weights, heads, scaling, residual):
    for _ in range(WARMUP_STEPS):
        attention_forward(x, weights, heads, scaling, residual)
    torch.cuda.synchronize()

    samples = []
    for _ in range(REPETITIONS):
        start = torch.cuda.Event(enable_timing=True)
        end = torch.cuda.Event(enable_timing=True)
        start.record()
        for _ in range(STEPS_PER_REPEAT):
            output, entropy = attention_forward(x, weights, heads, scaling, residual)
        end.record()
        torch.cuda.synchronize()
        samples.append(start.elapsed_time(end) / STEPS_PER_REPEAT)

    return {
        "heads": heads,
        "scaling": scaling,
        "residual": residual,
        "elapsed_ms_median": statistics.median(samples),
        "elapsed_ms_samples": samples,
        "output_l2": torch.linalg.vector_norm(output).item(),
        "attention_entropy": entropy.item(),
    }


def percent_change(value, baseline):
    return (value - baseline) / baseline * 100.0 if baseline else 0.0


def main():
    if not torch.cuda.is_available():
        raise RuntimeError("CUDA is required for this experiment")

    torch.manual_seed(SEED)
    torch.cuda.manual_seed_all(SEED)
    device = torch.device("cuda:0")
    x = torch.randn(BATCH_SIZE, SEQ_LEN, D_MODEL, device=device)
    weight_scale = 1.0 / math.sqrt(D_MODEL)
    weights = tuple(
        torch.randn(D_MODEL, D_MODEL, device=device) * weight_scale
        for _ in range(4)
    )

    configs = [
        (1, True, True),
        (2, True, True),
        (4, True, True),
        (8, True, True),
        (4, False, True),
        (4, True, False),
        (4, False, False),
    ]

    started_at = time.time()
    with torch.inference_mode():
        results = [benchmark_config(x, weights, *config) for config in configs]

    baseline = next(
        result
        for result in results
        if result["heads"] == 4 and result["scaling"] and result["residual"]
    )
    for result in results:
        result["change_vs_baseline"] = {
            "elapsed_ms_pct": percent_change(
                result["elapsed_ms_median"], baseline["elapsed_ms_median"]
            ),
            "output_l2_pct": percent_change(result["output_l2"], baseline["output_l2"]),
            "attention_entropy_pct": percent_change(
                result["attention_entropy"], baseline["attention_entropy"]
            ),
        }

    payload = {
        "experiment": "Attention Is All You Need lightweight GPU structural ablation",
        "scope": "forward_pass_smoke",
        "environment": {
            "torch_version": torch.__version__,
            "cuda_version": torch.version.cuda,
            "gpu_name": torch.cuda.get_device_name(0),
            "seed": SEED,
            "batch_size": BATCH_SIZE,
            "seq_len": SEQ_LEN,
            "d_model": D_MODEL,
            "warmup_steps": WARMUP_STEPS,
            "repetitions": REPETITIONS,
            "steps_per_repeat": STEPS_PER_REPEAT,
        },
        "baseline": baseline,
        "configs": results,
        "wall_time_seconds": time.time() - started_at,
        "limitations": [
            "Synthetic fixed input and weights; no model training or dataset download.",
            "This is not a WMT14 or BLEU reproduction.",
            "Latency is a single-V100 microbenchmark and should not be generalized to training throughput.",
        ],
    }
    print(json.dumps(payload, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
