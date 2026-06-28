#!/usr/bin/env python3
"""Terminal-Bench artifact post-processing for go-code.

This module keeps the Terminal-Bench adapter path honest:
- Terminal-Bench owns task pass/fail (`is_resolved` and parser results).
- The go-code adapter owns harness telemetry (`benchmark_result.json`).
- This postprocessor merges those streams into schema-validated JSONL.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import statistics
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_RESULTS_BASE = REPO_ROOT / ".tmp" / "terminal-bench"
DEFAULT_SCHEMA_PATH = REPO_ROOT / "benchmarks" / "comparison" / "result.schema.json"
DEFAULT_BASELINE_PATH = REPO_ROOT / "benchmarks" / "terminal_bench" / "baseline.json"
DEFAULT_DATASET_PATH = REPO_ROOT / "benchmarks" / "terminal_bench" / "tasks"


FAILURE_CLASSES = {
    "oracle_fail",
    "agent_timeout",
    "harness_error",
    "provider_error",
    "tool_contract_error",
    "workspace_error",
    "infra_error",
}


def load_json(path: Path) -> Any:
    return json.loads(path.read_text())


def write_json(path: Path, data: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n")


def find_latest_results_dir(base: Path = DEFAULT_RESULTS_BASE) -> Path | None:
    if not base.exists():
        return None
    candidates = sorted(base.iterdir(), key=lambda p: p.stat().st_mtime, reverse=True)
    for candidate in candidates:
        if candidate.is_dir():
            return candidate
    return None


def find_run_dir(results_dir: Path) -> Path | None:
    for child in sorted(results_dir.iterdir()):
        if child.is_dir() and "__" in child.name and (child / "results.json").exists():
            return child
    if (results_dir / "results.json").exists():
        return results_dir
    return None


def find_results_json(results_dir: Path) -> Path:
    run_dir = find_run_dir(results_dir)
    if run_dir is None:
        raise FileNotFoundError(f"no Terminal-Bench run dir with results.json under {results_dir}")
    results_path = run_dir / "results.json"
    if not results_path.exists():
        raise FileNotFoundError(f"results.json not found in {run_dir}")
    return results_path


def task_results(results: dict[str, Any]) -> list[dict[str, Any]]:
    raw = results.get("results", [])
    if not isinstance(raw, list):
        raise ValueError("Terminal-Bench results.json field 'results' must be a list")
    out: list[dict[str, Any]] = []
    for item in raw:
        if isinstance(item, dict):
            out.append(item)
    return out


def find_artifact(run_dir: Path, task_id: str, artifact_name: str) -> Path | None:
    task_dir = run_dir / task_id
    if not task_dir.exists():
        return None
    matches = sorted(task_dir.rglob(artifact_name))
    if not matches:
        return None
    agent_log_matches = [p for p in matches if p.parent.name == "agent-logs"]
    return agent_log_matches[0] if agent_log_matches else matches[0]


def load_benchmark_record(run_dir: Path, task_id: str) -> dict[str, Any] | None:
    path = find_artifact(run_dir, task_id, "benchmark_result.json")
    if path is None:
        return None
    record = load_json(path)
    if isinstance(record, dict):
        record.setdefault("artifact_path", str(path))
        return record
    return None


def load_telemetry(run_dir: Path, task_id: str) -> dict[str, Any] | None:
    path = find_artifact(run_dir, task_id, "harness_telemetry.json")
    if path is None:
        return None
    telemetry = load_json(path)
    if isinstance(telemetry, dict):
        return telemetry
    return None


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def synthetic_missing_record(task: dict[str, Any]) -> dict[str, Any]:
    task_id = str(task.get("task_id") or "unknown")
    ts = now_iso()
    return {
        "tool_id": "go-code",
        "task_id": task_id,
        "run_id": "missing-benchmark-result",
        "status": "failed",
        "steps_taken": 0,
        "total_prompt_tokens": int(task.get("total_input_tokens") or 0),
        "total_completion_tokens": int(task.get("total_output_tokens") or 0),
        "total_cost_usd": 0.0,
        "cost_status": "provider_unreported",
        "cache_hit_rate": 0.0,
        "model": str(task.get("model_name") or ""),
        "prompt": "",
        "created_at": ts,
        "updated_at": ts,
        "duration_ms": 0,
        "tool_calls": [],
        "error_message": "benchmark_result.json missing",
    }


def classify_failure(task: dict[str, Any], record: dict[str, Any]) -> str:
    if bool(task.get("is_resolved")):
        return ""
    run_id = str(record.get("run_id") or "")
    error = str(record.get("error_message") or record.get("error") or "").lower()
    status = str(record.get("status") or "").lower()
    if run_id in {"adapter-infra-error", "missing-benchmark-result"}:
        return "infra_error"
    if "deadline exceeded" in error or "timeout" in error or "timed out" in error:
        return "agent_timeout"
    if "request failed" in error or "rate limit" in error or "429" in error or "provider" in error:
        return "provider_error"
    if "apply_patch" in error or "tool" in error and ("required" in error or "schema" in error):
        return "tool_contract_error"
    if "workspace:" in error or "container" in error or "docker" in error:
        return "workspace_error"
    if status in {"failed", "cancelled"}:
        return "harness_error"
    return "oracle_fail"


def merge_task_result(task: dict[str, Any], record: dict[str, Any]) -> dict[str, Any]:
    merged = dict(record)
    merged.pop("artifact_path", None)
    merged["task_id"] = str(task.get("task_id") or merged.get("task_id") or "unknown")
    if "is_resolved" in task:
        merged["is_resolved"] = bool(task.get("is_resolved"))
    parser_results = task.get("parser_results")
    if isinstance(parser_results, dict):
        merged["parser_results"] = parser_results
    failure_class = classify_failure(task, merged)
    if failure_class:
        merged["failure_classification"] = failure_class
    return merged


def merge_oracle_results(run_dir: Path, output_path: Path) -> list[dict[str, Any]]:
    results = load_json(run_dir / "results.json")
    merged: list[dict[str, Any]] = []
    for task in task_results(results):
        task_id = str(task.get("task_id") or "")
        record = load_benchmark_record(run_dir, task_id) or synthetic_missing_record(task)
        merged.append(merge_task_result(task, record))
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with output_path.open("w") as f:
        for row in merged:
            f.write(json.dumps(row, sort_keys=True) + "\n")
    return merged


def validate_type(value: Any, expected: str) -> bool:
    if expected == "string":
        return isinstance(value, str)
    if expected == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    if expected == "number":
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    if expected == "boolean":
        return isinstance(value, bool)
    if expected == "array":
        return isinstance(value, list)
    if expected == "object":
        return isinstance(value, dict)
    return True


def validate_result_record(record: dict[str, Any], schema_path: Path = DEFAULT_SCHEMA_PATH) -> list[str]:
    schema = load_json(schema_path)
    errors: list[str] = []
    required = schema.get("required", [])
    properties = schema.get("properties", {})
    for field in required:
        if field not in record:
            errors.append(f"missing required field: {field}")
    if schema.get("additionalProperties") is False:
        allowed = set(properties.keys())
        for field in sorted(record.keys()):
            if field not in allowed:
                errors.append(f"additional property not allowed: {field}")
    for field, spec in properties.items():
        if field not in record:
            continue
        expected = spec.get("type")
        value = record[field]
        if isinstance(expected, list):
            if not any(validate_type(value, t) for t in expected):
                errors.append(f"{field}: expected one of {expected}, got {type(value).__name__}")
        elif isinstance(expected, str) and not validate_type(value, expected):
            errors.append(f"{field}: expected {expected}, got {type(value).__name__}")
        if "enum" in spec and value not in spec["enum"]:
            errors.append(f"{field}: expected one of {spec['enum']}, got {value!r}")
    return errors


def validate_results(records: list[dict[str, Any]], schema_path: Path = DEFAULT_SCHEMA_PATH) -> list[str]:
    errors: list[str] = []
    for idx, record in enumerate(records, 1):
        for err in validate_result_record(record, schema_path):
            errors.append(f"record {idx} ({record.get('task_id', '?')}): {err}")
    return errors


def load_baseline(path: Path = DEFAULT_BASELINE_PATH) -> dict[str, Any]:
    if not path.exists():
        return {"tasks": {}}
    data = load_json(path)
    if isinstance(data, dict):
        return data
    return {"tasks": {}}


def compare_to_baseline(records: list[dict[str, Any]], baseline: dict[str, Any]) -> dict[str, Any]:
    tasks = baseline.get("tasks", {}) if isinstance(baseline, dict) else {}
    regressions: list[str] = []
    improvements: list[str] = []
    new_tasks: list[str] = []
    resolved = [r for r in records if bool(r.get("is_resolved"))]
    durations = [int(r.get("duration_ms") or 0) for r in records]
    total_cost = round(sum(float(r.get("total_cost_usd") or 0.0) for r in records), 12)
    total_steps = sum(int(r.get("steps_taken") or 0) for r in records)
    total_tokens = sum(
        int(r.get("total_prompt_tokens") or 0) + int(r.get("total_completion_tokens") or 0)
        for r in records
    )
    timeouts = [r for r in records if r.get("failure_classification") == "agent_timeout"]
    for record in records:
        task_id = str(record.get("task_id") or "")
        passed = bool(record.get("is_resolved"))
        baseline_task = tasks.get(task_id)
        if baseline_task is None:
            new_tasks.append(task_id)
            continue
        expected = bool(baseline_task.get("expected_pass", True))
        if expected and not passed:
            regressions.append(task_id)
        if not expected and passed:
            improvements.append(task_id)
    n_total = len(records)
    n_resolved = len(resolved)
    return {
        "n_total": n_total,
        "n_resolved": n_resolved,
        "accuracy": (n_resolved / n_total) if n_total else 0.0,
        "regressions": sorted(regressions),
        "improvements": sorted(improvements),
        "new_tasks": sorted(new_tasks),
        "median_duration_ms": int(statistics.median(durations)) if durations else 0,
        "total_steps": total_steps,
        "total_tokens": total_tokens,
        "total_cost_usd": total_cost,
        "cost_per_resolved_task": round(total_cost / n_resolved, 12) if n_resolved else 0.0,
        "timeout_rate": (len(timeouts) / n_total) if n_total else 0.0,
    }


def failed_tests(task: dict[str, Any]) -> list[str]:
    parser_results = task.get("parser_results")
    if not isinstance(parser_results, dict):
        return []
    return [str(name) for name, result in parser_results.items() if result != "passed"]


def generate_report(
    run_dir: Path,
    records: list[dict[str, Any]],
    baseline_summary: dict[str, Any],
    terminal_results: dict[str, Any],
) -> str:
    lines: list[str] = []
    task_by_id = {str(t.get("task_id")): t for t in task_results(terminal_results)}
    pass_at_k = terminal_results.get("pass_at_k") or {}
    lines.append(f"# Terminal-Bench Report: {run_dir.name}")
    lines.append("")
    lines.append(f"- Accuracy: {baseline_summary['n_resolved']}/{baseline_summary['n_total']} ({baseline_summary['accuracy']:.1%})")
    if isinstance(pass_at_k, dict) and pass_at_k:
        pass_bits = ", ".join(f"pass@{k}: {float(v):.1%}" for k, v in sorted(pass_at_k.items()))
        lines.append(f"- {pass_bits}")
    lines.append(f"- Median duration: {baseline_summary['median_duration_ms']} ms")
    lines.append(f"- Total steps: {baseline_summary['total_steps']}")
    lines.append(f"- Total tokens: {baseline_summary['total_tokens']}")
    lines.append(f"- Total cost: ${baseline_summary['total_cost_usd']:.6f}")
    lines.append(f"- Cost per resolved task: ${baseline_summary['cost_per_resolved_task']:.6f}")
    lines.append(f"- Timeout rate: {baseline_summary['timeout_rate']:.1%}")
    lines.append("")
    lines.append("## Pass/Fail vs Baseline")
    lines.append("")
    lines.append("| Task | Result | Failure Class | Cost | Steps | Duration |")
    lines.append("|------|--------|---------------|------|-------|----------|")
    for record in sorted(records, key=lambda r: str(r.get("task_id"))):
        result = "PASS" if record.get("is_resolved") else "FAIL"
        failure = record.get("failure_classification", "")
        cost = float(record.get("total_cost_usd") or 0.0)
        steps = int(record.get("steps_taken") or 0)
        duration = int(record.get("duration_ms") or 0)
        lines.append(
            f"| {record.get('task_id')} | {result} | {failure} | ${cost:.6f} | {steps} | {duration} ms |"
        )
    lines.append("")
    if baseline_summary["regressions"]:
        lines.append(f"> **REGRESSION**: {', '.join(baseline_summary['regressions'])}")
        lines.append("")
    if baseline_summary["improvements"]:
        lines.append(f"> IMPROVEMENTS: {', '.join(baseline_summary['improvements'])}")
        lines.append("")
    failed = [r for r in records if not r.get("is_resolved")]
    if failed:
        lines.append("## Failure Analysis")
        lines.append("")
        for record in sorted(failed, key=lambda r: str(r.get("task_id"))):
            task_id = str(record.get("task_id"))
            task = task_by_id.get(task_id, {})
            lines.append(f"### {task_id}")
            lines.append("")
            lines.append(f"- Classification: {record.get('failure_classification', 'oracle_fail')}")
            lines.append(f"- Harness status: {record.get('status', '')}")
            lines.append(f"- Steps: {record.get('steps_taken', 0)}")
            lines.append(f"- Cost: ${float(record.get('total_cost_usd') or 0.0):.6f}")
            tests = failed_tests(task)
            if tests:
                lines.append(f"- Failed tests: {', '.join(tests)}")
            tool_calls = record.get("tool_calls") or []
            if tool_calls:
                last_tools = [str(tc.get("tool_name", "?")) for tc in tool_calls[-3:] if isinstance(tc, dict)]
                if last_tools:
                    lines.append(f"- Last tool calls: {', '.join(last_tools)}")
            rollout_path = record.get("rollout_path")
            if rollout_path:
                lines.append(f"- Replay: `curl -fsS -X POST /v1/runs/replay -d '{{\"mode\":\"simulate\",\"detect_drift\":true,\"rollout_path\":\"{rollout_path}\"}}'`")
            error_message = record.get("error_message")
            if error_message:
                lines.append(f"- Error: `{error_message}`")
            lines.append("")
    lines.append("## Artifacts")
    lines.append("")
    lines.append(f"- Raw Terminal-Bench results: `{run_dir / 'results.json'}`")
    lines.append(f"- Merged JSONL: `{run_dir / 'results.jsonl'}`")
    lines.append(f"- Run environment: `{run_dir / 'run-env.json'}`")
    lines.append("")
    lines.append(f"_Generated at {now_iso()}_")
    return "\n".join(lines)


def git_sha() -> str:
    try:
        return subprocess.check_output(
            ["git", "-C", str(REPO_ROOT), "rev-parse", "--short", "HEAD"],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
    except Exception:
        return ""


def command_output(args: list[str]) -> str:
    try:
        return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT).strip()
    except Exception:
        return ""


def dataset_hash(dataset_path: Path = DEFAULT_DATASET_PATH) -> str:
    h = hashlib.sha256()
    if not dataset_path.exists():
        return ""
    for path in sorted(p for p in dataset_path.rglob("*") if p.is_file()):
        rel = path.relative_to(dataset_path).as_posix()
        h.update(rel.encode())
        h.update(b"\0")
        h.update(path.read_bytes())
        h.update(b"\0")
    return h.hexdigest()


def write_run_env(
    run_dir: Path,
    *,
    model: str,
    provider: str,
    dataset_path: Path,
    n_concurrent: str,
    n_attempts: str,
    agent_timeout: str,
    test_timeout: str,
    cleanup: str,
) -> dict[str, Any]:
    env = {
        "generated_at": now_iso(),
        "git_sha": git_sha(),
        "model": model,
        "provider": provider,
        "dataset_path": str(dataset_path),
        "dataset_hash": dataset_hash(dataset_path),
        "terminal_bench_version": os.getenv("TERMINAL_BENCH_VERSION") or command_output(["tb", "--version"]),
        "n_concurrent": n_concurrent,
        "n_attempts": n_attempts,
        "global_agent_timeout_sec": agent_timeout,
        "global_test_timeout_sec": test_timeout,
        "cleanup": cleanup,
    }
    write_json(run_dir / "run-env.json", env)
    return env


def postprocess(results_dir: Path, output_path: Path | None = None, report_path: Path | None = None) -> int:
    run_dir = find_run_dir(results_dir)
    if run_dir is None:
        print(f"terminal-bench artifacts: no run dir found under {results_dir}", file=sys.stderr)
        return 1
    output_path = output_path or (run_dir / "results.jsonl")
    report_path = report_path or (run_dir / "report.md")
    records = merge_oracle_results(run_dir, output_path)
    errors = validate_results(records)
    if errors:
        for err in errors:
            print(f"schema error: {err}", file=sys.stderr)
        return 1
    baseline = load_baseline()
    summary = compare_to_baseline(records, baseline)
    terminal_results = load_json(run_dir / "results.json")
    report = generate_report(run_dir, records, summary, terminal_results)
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(report + "\n")
    write_json(run_dir / "summary.json", summary)
    print(f"[terminal-bench] merged JSONL: {output_path}")
    print(f"[terminal-bench] report: {report_path}")
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Post-process go-code Terminal-Bench artifacts")
    parser.add_argument("results_dir", nargs="?", type=Path, help="Terminal-Bench output directory")
    parser.add_argument("--output-jsonl", type=Path, help="Merged JSONL output path")
    parser.add_argument("--report", type=Path, help="Markdown report output path")
    parser.add_argument("--write-run-env", action="store_true", help="Write run-env.json and exit")
    parser.add_argument("--model", default="")
    parser.add_argument("--provider", default="")
    parser.add_argument("--dataset-path", type=Path, default=DEFAULT_DATASET_PATH)
    parser.add_argument("--n-concurrent", default="")
    parser.add_argument("--n-attempts", default="")
    parser.add_argument("--global-agent-timeout-sec", default="")
    parser.add_argument("--global-test-timeout-sec", default="")
    parser.add_argument("--cleanup", default="")
    args = parser.parse_args(argv)

    results_dir = args.results_dir or find_latest_results_dir()
    if results_dir is None:
        print("terminal-bench artifacts: no results directory found", file=sys.stderr)
        return 1
    if args.write_run_env:
        run_dir = find_run_dir(results_dir) or results_dir
        write_run_env(
            run_dir,
            model=args.model,
            provider=args.provider,
            dataset_path=args.dataset_path,
            n_concurrent=args.n_concurrent,
            n_attempts=args.n_attempts,
            agent_timeout=args.global_agent_timeout_sec,
            test_timeout=args.global_test_timeout_sec,
            cleanup=args.cleanup,
        )
        print(f"[terminal-bench] run-env: {run_dir / 'run-env.json'}")
        return 0
    return postprocess(results_dir, args.output_jsonl, args.report)


if __name__ == "__main__":
    raise SystemExit(main())
