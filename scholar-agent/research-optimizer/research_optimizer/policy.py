from __future__ import annotations

import json
import math
from typing import Any

SELECTION_REQUEST_VERSION = "research-optimizer.selection-request/v1"
SELECTION_VERSION = "research-optimizer.selection/v1"
MODEL_ENUMERATION_POLICY = "bounded-exhaustive/v1"
POLICY_VERSION = "hierarchical-contextual-ucb-uct/v2"
MODEL_DEFAULTS_PHASE = "model_defaults"
PARAMETER_SEARCH_PHASE = "parameter_search"


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


def _mean(total: float, weight: float) -> float:
    return total / weight if weight > 0 else 0.0


def _top_k_mean(values: list[float], limit: int = 3) -> float:
    if not values:
        return 0.0
    selected = sorted(values, reverse=True)[:limit]
    return sum(selected) / len(selected)


def _model_default_response(candidate: dict[str, Any]) -> dict[str, Any]:
    return {
        "version": SELECTION_VERSION,
        "policy_version": MODEL_ENUMERATION_POLICY,
        "candidate_id": str(candidate["id"]),
        "route": str(candidate.get("strategy") or ""),
        "frontier_kind": "beam",
        "beam_rank": 1,
        "route_visit_count": 0,
        "route_mean_reward": 0.0,
        "route_top_k_mean_reward": 0.0,
        "route_best_reward": 0.0,
        "route_exploration_bonus": 0.0,
        "node_visit_count": 0,
        "node_mean_reward": 0.0,
        "node_exploration_bonus": 0.0,
        "virtual_visits": 0,
        "selection_score": 0.0,
        "propensity": 1.0,
        "predicted_reward": 0.0,
        "reason_codes": ["model_default_required", "bounded_exhaustive_enumeration"],
    }


def select_candidate(request: dict[str, Any], experiences: list[dict[str, Any]]) -> dict[str, Any]:
    if request.get("version") != SELECTION_REQUEST_VERSION:
        raise PolicyError("unsupported selection request version")
    candidates = request.get("candidates")
    if not isinstance(candidates, list) or not candidates or len(candidates) > 256:
        raise PolicyError("selection requires 1 to 256 candidates")
    identifiers = [str(item.get("id") or "") for item in candidates]
    if any(not value for value in identifiers) or len(set(identifiers)) != len(identifiers):
        raise PolicyError("candidate identifiers are missing or duplicated")
    candidate_hints = request.get("candidate_hints") or []
    hint_by_id = {str(item.get("candidate_id") or ""): item for item in candidate_hints}
    if set(hint_by_id) != set(identifiers):
        raise PolicyError("candidate hints must cover the complete frontier")
    for hint in hint_by_id.values():
        if hint.get("frontier_kind") not in {"beam", "exploration"}:
            raise PolicyError("candidate hint has an invalid frontier kind")
    phase = str(request.get("phase") or "")
    if phase not in {MODEL_DEFAULTS_PHASE, PARAMETER_SEARCH_PHASE}:
        raise PolicyError("selection phase is unsupported")
    if phase == MODEL_DEFAULTS_PHASE:
        response = _model_default_response(candidates[0])
        json.dumps(response, allow_nan=False)
        return response

    context = request.get("context") or {}
    history = request.get("history") or []
    in_flight = request.get("in_flight") or []
    if not isinstance(in_flight, list) or len(in_flight) > 4:
        raise PolicyError("in_flight must contain at most four reserved candidates")

    route_sums: dict[str, float] = {}
    route_weights: dict[str, float] = {}
    route_visits: dict[str, int] = {}
    route_values: dict[str, list[float]] = {}
    node_sums: dict[str, float] = {}
    node_weights: dict[str, float] = {}
    node_visits: dict[str, int] = {}
    contextual_sums: dict[str, float] = {}
    contextual_weights: dict[str, float] = {}
    for trial in history:
        candidate = trial.get("candidate") or {}
        strategy = str(candidate.get("strategy") or "")
        reward = trial.get("reward")
        if not strategy or not isinstance(reward, (int, float)) or not math.isfinite(float(reward)):
            continue
        value = float(reward)
        route_sums[strategy] = route_sums.get(strategy, 0.0) + value
        route_weights[strategy] = route_weights.get(strategy, 0.0) + 1.0
        route_visits[strategy] = route_visits.get(strategy, 0) + 1
        route_values.setdefault(strategy, []).append(value)
        path = trial.get("backprop_path") or [candidate.get("id")]
        for node_id in path:
            node_id = str(node_id or "")
            if not node_id:
                continue
            node_sums[node_id] = node_sums.get(node_id, 0.0) + value
            node_weights[node_id] = node_weights.get(node_id, 0.0) + 1.0
            node_visits[node_id] = node_visits.get(node_id, 0) + 1

    available_routes = list(dict.fromkeys(str(item.get("strategy") or "") for item in candidates))
    contextual_weight = 0.0
    for experience in experiences:
        strategy = str(experience.get("strategy") or "")
        reward = experience.get("reward")
        if strategy not in available_routes or not isinstance(reward, (int, float)) or not math.isfinite(float(reward)):
            continue
        weight = 0.35 * _context_similarity(context, experience.get("context") or {})
        if weight <= 0:
            continue
        contextual_sums[strategy] = contextual_sums.get(strategy, 0.0) + weight * float(reward)
        contextual_weights[strategy] = contextual_weights.get(strategy, 0.0) + weight

    # Cross-task history is a prior, not a substitute for this campaign's
    # measured defaults. Cap each route below one real observation.
    for strategy, raw_weight in list(contextual_weights.items()):
        capped_weight = min(0.75, raw_weight)
        prior_mean = _mean(contextual_sums[strategy], raw_weight)
        contextual_sums[strategy] = prior_mean * capped_weight
        contextual_weights[strategy] = capped_weight
        route_sums[strategy] = route_sums.get(strategy, 0.0) + prior_mean * capped_weight
        route_weights[strategy] = route_weights.get(strategy, 0.0) + capped_weight
        contextual_weight += capped_weight

    route_virtual: dict[str, int] = {}
    node_virtual: dict[str, int] = {}
    for candidate in in_flight:
        strategy = str(candidate.get("strategy") or "")
        route_virtual[strategy] = route_virtual.get(strategy, 0) + 1
        parent_id = str(candidate.get("parent_id") or candidate.get("id") or "")
        node_virtual[parent_id] = node_virtual.get(parent_id, 0) + 1

    exploration_scale = 0.35
    virtual_loss = 0.05
    total_weight = sum(route_weights.values())
    route_scores: dict[str, float] = {}
    route_bonuses: dict[str, float] = {}
    route_top_k: dict[str, float] = {}
    route_portfolios: dict[str, float] = {}
    for route in available_routes:
        weight = route_weights.get(route, 0.0)
        virtual = route_virtual.get(route, 0)
        bonus = exploration_scale * math.sqrt(
            math.log(total_weight + len(in_flight) + 2.0) / max(1.0, weight + virtual)
        )
        top_k = _top_k_mean(route_values.get(route, []))
        observed_weight = float(min(3, len(route_values.get(route, []))))
        prior_weight = contextual_weights.get(route, 0.0)
        portfolio = _mean(top_k * observed_weight + contextual_sums.get(route, 0.0), observed_weight + prior_weight)
        route_top_k[route] = top_k
        route_portfolios[route] = portfolio
        route_bonuses[route] = bonus
        route_scores[route] = portfolio + bonus - virtual_loss * virtual
    selected_route = max(available_routes, key=lambda route: (route_scores[route], -available_routes.index(route)))

    selected_index = -1
    selected_parent = ""
    selected_node_score = -math.inf
    selected_node_bonus = 0.0
    selected_node_mean = 0.0
    for index, candidate in enumerate(candidates):
        if str(candidate.get("strategy") or "") != selected_route:
            continue
        parent_id = str(candidate.get("parent_id") or candidate.get("id") or "")
        weight = node_weights.get(parent_id, 0.0)
        virtual = node_virtual.get(parent_id, 0)
        bonus = exploration_scale * math.sqrt(
            math.log(route_weights.get(selected_route, 0.0) + route_virtual.get(selected_route, 0) + 2.0)
            / max(1.0, weight + virtual)
        )
        mean = _mean(node_sums.get(parent_id, 0.0), weight)
        score = mean + bonus - virtual_loss * virtual
        if score > selected_node_score:
            selected_index = index
            selected_parent = parent_id
            selected_node_score = score
            selected_node_bonus = bonus
            selected_node_mean = mean
    if selected_index < 0:
        raise PolicyError("selected route has no candidate")

    route_mean = _mean(route_sums.get(selected_route, 0.0), route_weights.get(selected_route, 0.0))
    route_rewards = route_values.get(selected_route, [])
    route_best = max(route_rewards) if route_rewards else 0.0
    virtual_visits = route_virtual.get(selected_route, 0) + node_virtual.get(selected_parent, 0)
    reason_codes = ["outer_contextual_ucb_route", "inner_uct_parameter_path", "route_top_k_portfolio"]
    selected_hint = hint_by_id[identifiers[selected_index]]
    if selected_hint["frontier_kind"] == "beam":
        reason_codes.append("top_k_beam_parent")
    else:
        reason_codes.append("low_visit_exploration_lane")
    if contextual_weight > 0:
        reason_codes.append("validated_contextual_prior")
    if virtual_visits > 0:
        reason_codes.append("in_flight_virtual_visit_penalty")
    response = {
        "version": SELECTION_VERSION,
        "policy_version": POLICY_VERSION,
        "candidate_id": identifiers[selected_index],
        "route": selected_route,
        "frontier_kind": selected_hint["frontier_kind"],
        "beam_rank": int(selected_hint.get("beam_rank") or 0),
        "route_visit_count": route_visits.get(selected_route, 0),
        "route_mean_reward": route_mean,
        "route_top_k_mean_reward": route_top_k[selected_route],
        "route_best_reward": route_best,
        "route_exploration_bonus": route_bonuses[selected_route],
        "node_visit_count": node_visits.get(selected_parent, 0),
        "node_mean_reward": selected_node_mean,
        "node_exploration_bonus": selected_node_bonus,
        "virtual_visits": virtual_visits,
        "selection_score": route_scores[selected_route] + selected_node_score,
        "propensity": 1.0,
        "predicted_reward": (route_portfolios[selected_route] + selected_node_mean) / 2.0,
        "reason_codes": reason_codes,
    }
    json.dumps(response, allow_nan=False)
    return response
