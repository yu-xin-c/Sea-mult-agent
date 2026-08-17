from __future__ import annotations

import hashlib
import json
import math
import random
from typing import Any

SELECTION_REQUEST_VERSION = "research-optimizer.selection-request/v1"
SELECTION_VERSION = "research-optimizer.selection/v1"
POLICY_VERSION = "contextual-ucb/v1"


class PolicyError(ValueError):
    pass


def _context_similarity(left: dict[str, Any], right: dict[str, Any]) -> float:
    if left.get("domain") != right.get("domain") or left.get("adapter") != right.get("adapter"):
        return 0.0
    scores: list[float] = []
    left_numeric = left.get("numeric") or {}
    right_numeric = right.get("numeric") or {}
    for key in sorted(set(left_numeric) & set(right_numeric)):
        a, b = float(left_numeric[key]), float(right_numeric[key])
        scale = max(abs(a), abs(b), 1.0)
        scores.append(1.0 / (1.0 + abs(a - b) / scale))
    left_boolean = left.get("boolean") or {}
    right_boolean = right.get("boolean") or {}
    for key in sorted(set(left_boolean) & set(right_boolean)):
        scores.append(1.0 if bool(left_boolean[key]) == bool(right_boolean[key]) else 0.0)
    return sum(scores) / len(scores) if scores else 1.0


def _seed(request: dict[str, Any]) -> int:
    value = f"{request.get('campaign_id')}:{request.get('trial_number')}".encode("utf-8")
    return int.from_bytes(hashlib.sha256(value).digest()[:8], "big")


def select_candidate(request: dict[str, Any], experiences: list[dict[str, Any]]) -> dict[str, Any]:
    if request.get("version") != SELECTION_REQUEST_VERSION:
        raise PolicyError("unsupported selection request version")
    candidates = request.get("candidates")
    if not isinstance(candidates, list) or not candidates or len(candidates) > 256:
        raise PolicyError("selection requires 1 to 256 candidates")
    identifiers = [str(item.get("id") or "") for item in candidates]
    if any(not value for value in identifiers) or len(set(identifiers)) != len(identifiers):
        raise PolicyError("candidate identifiers are missing or duplicated")
    context = request.get("context") or {}

    current_rewards: dict[str, list[float]] = {}
    for trial in request.get("history") or []:
        candidate = trial.get("candidate") or {}
        reward = trial.get("reward")
        if candidate.get("id") and isinstance(reward, (int, float)):
            current_rewards.setdefault(str(candidate["id"]), []).append(float(reward))

    estimates: list[tuple[float, float, float]] = []
    total_weight = 0.0
    for candidate in candidates:
        candidate_id = str(candidate["id"])
        strategy = str(candidate.get("strategy") or "")
        weighted_reward = 0.0
        weight = 0.0
        for experience in experiences:
            action_weight = 1.0 if experience.get("candidate_id") == candidate_id else 0.35 if experience.get("strategy") == strategy else 0.0
            if action_weight == 0:
                continue
            similarity = _context_similarity(context, experience.get("context") or {})
            sample_weight = action_weight * similarity
            weighted_reward += sample_weight * float(experience["reward"])
            weight += sample_weight
        for reward in current_rewards.get(candidate_id, []):
            weighted_reward += reward
            weight += 1.0
        mean = weighted_reward / weight if weight else 0.0
        total_weight += weight
        estimates.append((mean, weight, 0.0))

    if total_weight == 0:
        rng = random.Random(_seed(request))
        selected_index = rng.randrange(len(candidates))
        propensity = 1.0 / len(candidates)
        reason_codes = ["cold_start_uniform_exploration"]
        predicted_reward = 0.0
    else:
        exploration_scale = 0.15
        scored: list[float] = []
        for mean, weight, _ in estimates:
            bonus = exploration_scale * math.sqrt(math.log(total_weight + 2.0) / (weight + 1.0))
            scored.append(mean + bonus)
        greedy_index = max(range(len(scored)), key=lambda index: (scored[index], -index))
        epsilon = 0.10
        rng = random.Random(_seed(request))
        if rng.random() < epsilon:
            selected_index = rng.randrange(len(candidates))
            reason_codes = ["epsilon_exploration", "contextual_history_available"]
        else:
            selected_index = greedy_index
            reason_codes = ["contextual_ucb_exploitation", "validated_history_available"]
        propensity = epsilon / len(candidates)
        if selected_index == greedy_index:
            propensity += 1.0 - epsilon
        predicted_reward = estimates[selected_index][0]

    response = {
        "version": SELECTION_VERSION,
        "policy_version": POLICY_VERSION,
        "candidate_id": identifiers[selected_index],
        "propensity": propensity,
        "predicted_reward": predicted_reward,
        "reason_codes": reason_codes,
    }
    json.dumps(response, allow_nan=False)
    return response
