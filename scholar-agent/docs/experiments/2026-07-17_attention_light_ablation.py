import subprocess
import sys
import json
import math
import random
import time

# Check GPU info via nvidia-smi
gpu_info = ""
try:
    result = subprocess.run(["nvidia-smi"], capture_output=True, text=True, timeout=10)
    gpu_info = result.stdout.strip()
except Exception:
    gpu_info = "nvidia-smi not available"

compute_device = "cpu"

# Fixed seed
random.seed(42)

# Dimensions
seq_len = 12
d_model = 48
num_runs = 5

def softmax(logits):
    max_val = max(logits)
    exps = [math.exp(x - max_val) for x in logits]
    sum_exps = sum(exps)
    return [e / sum_exps for e in exps]

def scaled_dot_product_attention(Q, K, V, scale=True):
    # Q, K, V: list of lists (seq_len x head_dim)
    d_k = len(Q[0])
    # Compute attention scores
    scores = []
    for i in range(len(Q)):
        row = []
        for j in range(len(K)):
            s = sum(Q[i][k] * K[j][k] for k in range(d_k))
            row.append(s)
        scores.append(row)
    if scale:
        scale_factor = math.sqrt(d_k)
        for i in range(len(scores)):
            for j in range(len(scores[i])):
                scores[i][j] /= scale_factor
    # Softmax per row
    att_weights = [softmax(row) for row in scores]
    # Compute output
    output = []
    for i in range(len(att_weights)):
        out_row = [0.0] * d_k
        for j in range(len(V)):
            for k in range(d_k):
                out_row[k] += att_weights[i][j] * V[j][k]
        output.append(out_row)
    # Compute entropy per head: average entropy across positions
    entropy_sum = 0.0
    for row in att_weights:
        for p in row:
            if p > 0:
                entropy_sum -= p * math.log2(p)
    avg_entropy = entropy_sum / len(att_weights)
    return output, avg_entropy

def multi_head_self_attention(x, num_heads, scale=True, residual=True):
    # x: seq_len x d_model
    seq, dim = len(x), len(x[0])
    head_dim = dim // num_heads
    # Generate random projections (fixed seed for repeatability, but we keep random.seed(42) at top; ok)
    # Use fixed weights for consistency across configs (make deterministic with seed)
    local_random = random.Random(42)
    # Initialize W_Q, W_K, W_V as lists of lists
    W_Q = [[0.0] * dim for _ in range(dim)]
    W_K = [[0.0] * dim for _ in range(dim)]
    W_V = [[0.0] * dim for _ in range(dim)]
    for i in range(dim):
        for j in range(dim):
            W_Q[i][j] = local_random.gauss(0, 0.02)
            W_K[i][j] = local_random.gauss(0, 0.02)
            W_V[i][j] = local_random.gauss(0, 0.02)
    W_O = [[0.0] * dim for _ in range(dim)]
    for i in range(dim):
        for j in range(dim):
            W_O[i][j] = local_random.gauss(0, 0.02)

    # Linear projections
    def linear(x, W):
        out = []
        for i in range(len(x)):
            row = [0.0] * len(W[0])
            for k in range(len(W)):
                for j in range(len(W[0])):
                    row[j] += x[i][k] * W[k][j]
            out.append(row)
        return out

    Q = linear(x, W_Q)
    K = linear(x, W_K)
    V = linear(x, W_V)

    # Split heads
    def split_heads(mat):
        heads = []
        for h in range(num_heads):
            head = []
            for i in range(seq):
                head.append(mat[i][h*head_dim : (h+1)*head_dim])
            heads.append(head)
        return heads

    Q_heads = split_heads(Q)
    K_heads = split_heads(K)
    V_heads = split_heads(V)

    # Apply attention per head
    head_outputs = []
    total_entropy = 0.0
    for h in range(num_heads):
        out_h, entropy_h = scaled_dot_product_attention(Q_heads[h], K_heads[h], V_heads[h], scale)
        head_outputs.append(out_h)
        total_entropy += entropy_h
    avg_attention_entropy = total_entropy / num_heads

    # Concatenate heads
    concat = []
    for i in range(seq):
        row = []
        for h in range(num_heads):
            row.extend(head_outputs[h][i])
        concat.append(row)

    # Output projection
    out_before_residual = linear(concat, W_O)

    # Residual connection
    if residual:
        output = []
        for i in range(seq):
            out_row = [out_before_residual[i][j] + x[i][j] for j in range(dim)]
            output.append(out_row)
    else:
        output = out_before_residual

    return output, avg_attention_entropy

def l2_norm(mat):
    s = 0.0
    for row in mat:
        for val in row:
            s += val * val
    return math.sqrt(s)

# Generate random input
random_input = random.Random(42)
x = [[random_input.gauss(0, 0.5) for _ in range(d_model)] for _ in range(seq_len)]

# Baseline config: heads=4, scale=True, residual=True
baseline_output = None
baseline_entropy = None
baseline_times = []
for _ in range(num_runs):
    start = time.perf_counter()
    out, ent = multi_head_self_attention(x, num_heads=4, scale=True, residual=True)
    elapsed = (time.perf_counter() - start) * 1000
    baseline_times.append(elapsed)
baseline_output = out
baseline_entropy = ent
baseline_elapsed_ms = sum(baseline_times) / len(baseline_times)

results = {"environment": {"compute_device": compute_device, "gpu_info": gpu_info, "seed": 42}, "configs": [], "baseline": {"heads": 4, "scaling": True, "residual": True, "elapsed_ms": baseline_elapsed_ms, "l2": l2_norm(baseline_output), "attention_entropy": baseline_entropy}}

configs = [
    (1, True, True),
    (2, True, True),
    (4, True, True),
    (8, True, True),
    (4, False, True),
    (4, True, False),
    (4, False, False),
]

for heads, scaling, residual in configs:
    times = []
    outputs = []
    entropies = []
    for run in range(num_runs):
        start = time.perf_counter()
        out, ent = multi_head_self_attention(x, num_heads=heads, scale=scaling, residual=residual)
        elapsed = (time.perf_counter() - start) * 1000
        times.append(elapsed)
        outputs.append(out)
        entropies.append(ent)
    avg_time = sum(times) / len(times)
    avg_out = [[sum(outputs[r][i][j] for r in range(num_runs)) / num_runs for j in range(d_model)] for i in range(seq_len)]
    avg_entropy = sum(entropies) / len(entropies)
    avg_l2 = l2_norm(avg_out)

    # Compute changes relative to baseline
    # For elapsed: (avg_time - baseline_elapsed) / baseline_elapsed * 100
    # For output_l2: (avg_l2 - baseline_l2) / baseline_l2 * 100 (if baseline l2 != 0)
    bl_l2 = l2_norm(baseline_output)
    change_elapsed = ((avg_time - baseline_elapsed_ms) / baseline_elapsed_ms) * 100
    change_l2 = ((avg_l2 - bl_l2) / bl_l2) * 100 if bl_l2 != 0 else 0.0
    change_entropy = ((avg_entropy - baseline_entropy) / baseline_entropy) * 100 if baseline_entropy != 0 else 0.0

    config_entry = {
        "heads": heads,
        "scaling": scaling,
        "residual": residual,
        "elapsed_ms": avg_time,
        "output_l2": avg_l2,
        "attention_entropy": avg_entropy,
        "change_vs_baseline": {
            "elapsed_ms_pct": change_elapsed,
            "output_l2_pct": change_l2,
            "attention_entropy_pct": change_entropy
        }
    }
    results["configs"].append(config_entry)

print(json.dumps(results, indent=2))
