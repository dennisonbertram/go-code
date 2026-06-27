#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import tempfile
import types
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
MODULE_PATH = REPO_ROOT / "scripts" / "terminal_bench_artifacts.py"


def load_module():
    spec = importlib.util.spec_from_file_location("terminal_bench_artifacts", MODULE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"could not load {MODULE_PATH}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_agent_module():
    root = types.ModuleType("terminal_bench")
    agents = types.ModuleType("terminal_bench.agents")
    base_agent = types.ModuleType("terminal_bench.agents.base_agent")
    failure_mode = types.ModuleType("terminal_bench.agents.failure_mode")
    terminal = types.ModuleType("terminal_bench.terminal")
    tmux_session = types.ModuleType("terminal_bench.terminal.tmux_session")

    class BaseAgent:
        def __init__(self, **_: object) -> None:
            pass

    class AgentResult:
        def __init__(self, **kwargs: object) -> None:
            self.__dict__.update(kwargs)

    class FailureMode:
        NONE = "none"
        AGENT_INSTALLATION_FAILED = "agent_installation_failed"
        UNKNOWN_AGENT_ERROR = "unknown_agent_error"

    class TmuxSession:
        pass

    base_agent.BaseAgent = BaseAgent
    base_agent.AgentResult = AgentResult
    failure_mode.FailureMode = FailureMode
    tmux_session.TmuxSession = TmuxSession

    original = {
        name: sys.modules.get(name)
        for name in [
            "terminal_bench",
            "terminal_bench.agents",
            "terminal_bench.agents.base_agent",
            "terminal_bench.agents.failure_mode",
            "terminal_bench.terminal",
            "terminal_bench.terminal.tmux_session",
        ]
    }
    sys.modules.update(
        {
            "terminal_bench": root,
            "terminal_bench.agents": agents,
            "terminal_bench.agents.base_agent": base_agent,
            "terminal_bench.agents.failure_mode": failure_mode,
            "terminal_bench.terminal": terminal,
            "terminal_bench.terminal.tmux_session": tmux_session,
        }
    )
    try:
        module_path = REPO_ROOT / "benchmarks" / "terminal_bench" / "agent.py"
        spec = importlib.util.spec_from_file_location("terminal_bench_agent_test", module_path)
        if spec is None or spec.loader is None:
            raise RuntimeError(f"could not load {module_path}")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    finally:
        for name, value in original.items():
            if value is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = value


class TerminalBenchArtifactsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module()

    def test_merge_oracle_writes_schema_valid_jsonl(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            run_dir = root / "2026-06-26__10-30-00"
            trial_dir = run_dir / "task-one" / "trial-1" / "agent-logs"
            trial_dir.mkdir(parents=True)
            (run_dir / "results.json").write_text(
                json.dumps(
                    {
                        "results": [
                            {
                                "task_id": "task-one",
                                "is_resolved": True,
                                "parser_results": {"test_ok": "passed"},
                            }
                        ],
                        "n_resolved": 1,
                        "accuracy": 1.0,
                    }
                )
            )
            record = {
                "tool_id": "go-code",
                "task_id": "task-one",
                "run_id": "run_123",
                "status": "completed",
                "steps_taken": 2,
                "total_prompt_tokens": 10,
                "total_completion_tokens": 5,
                "total_cost_usd": 0.01,
                "cost_status": "available",
                "cache_hit_rate": 0.0,
                "model": "fake-model",
                "prompt": "fix it",
                "created_at": "2026-06-26T14:30:00Z",
                "updated_at": "2026-06-26T14:31:00Z",
                "duration_ms": 60000,
                "tool_calls": [{"tool_name": "bash", "step": 1}],
            }
            (trial_dir / "benchmark_result.json").write_text(json.dumps(record))

            out_path = root / "merged.jsonl"
            merged = self.module.merge_oracle_results(run_dir, out_path)

            self.assertEqual(merged[0]["is_resolved"], True)
            self.assertEqual(merged[0]["parser_results"], {"test_ok": "passed"})
            rows = [json.loads(line) for line in out_path.read_text().splitlines()]
            self.assertEqual(rows, merged)
            errors = self.module.validate_result_record(rows[0])
            self.assertEqual(errors, [])

    def test_failure_classification_prefers_specific_harness_signals(self) -> None:
        classify = self.module.classify_failure
        self.assertEqual(classify({"is_resolved": False}, {"status": "completed"}), "oracle_fail")
        self.assertEqual(classify({"is_resolved": False}, {"error_message": "context deadline exceeded"}), "agent_timeout")
        self.assertEqual(classify({"is_resolved": False}, {"error_message": "openai request failed (429): rate"}), "provider_error")
        self.assertEqual(classify({"is_resolved": False}, {"error_message": "apply_patch path is required"}), "tool_contract_error")
        self.assertEqual(classify({"is_resolved": False}, {"error_message": "workspace: container create: boom"}), "workspace_error")
        self.assertEqual(classify({"is_resolved": False}, {"run_id": "adapter-infra-error"}), "infra_error")

    def test_baseline_comparison_reports_regressions_and_metrics(self) -> None:
        baseline = {
            "tasks": {
                "task-one": {"expected_pass": True},
                "task-two": {"expected_pass": False},
            }
        }
        records = [
            {
                "task_id": "task-one",
                "is_resolved": False,
                "duration_ms": 3000,
                "steps_taken": 4,
                "total_prompt_tokens": 10,
                "total_completion_tokens": 2,
                "total_cost_usd": 0.02,
                "status": "completed",
            },
            {
                "task_id": "task-two",
                "is_resolved": True,
                "duration_ms": 1000,
                "steps_taken": 2,
                "total_prompt_tokens": 4,
                "total_completion_tokens": 1,
                "total_cost_usd": 0.01,
                "status": "completed",
            },
        ]

        summary = self.module.compare_to_baseline(records, baseline)

        self.assertEqual(summary["accuracy"], 0.5)
        self.assertEqual(summary["regressions"], ["task-one"])
        self.assertEqual(summary["improvements"], ["task-two"])
        self.assertEqual(summary["median_duration_ms"], 2000)
        self.assertEqual(summary["total_cost_usd"], 0.03)
        self.assertEqual(summary["cost_per_resolved_task"], 0.03)


class TerminalBenchRunnerPreflightTest(unittest.TestCase):
    def test_preflight_only_accepts_fake_provider_without_openai_key(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            tmp = Path(td)
            bin_dir = tmp / "bin"
            bin_dir.mkdir()
            turns = tmp / "turns.json"
            turns.write_text('[{"content":"ok"}]\n')
            for name in ["docker", "tmux"]:
                path = bin_dir / name
                path.write_text("#!/usr/bin/env bash\nexit 0\n")
                path.chmod(0o755)
            tb = bin_dir / "tb"
            tb.write_text("#!/usr/bin/env bash\nif [[ ${1:-} == '--help' ]]; then echo tb-test; fi\nexit 0\n")
            tb.chmod(0o755)

            env = {
                "PATH": f"{bin_dir}:{__import__('os').environ['PATH']}",
                "HARNESS_PROVIDER": "fake",
                "HARNESS_FAKE_TURNS": str(turns),
                "TERMINAL_BENCH_DATASET_PATH": str(REPO_ROOT / "benchmarks" / "terminal_bench" / "tasks"),
            }
            result = subprocess.run(
                ["bash", str(REPO_ROOT / "scripts" / "run-terminal-bench.sh"), "--preflight-only"],
                cwd=REPO_ROOT,
                env=env,
                capture_output=True,
                text=True,
                timeout=15,
            )

            self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
            self.assertIn("preflight ok", result.stderr + result.stdout)


class TerminalBenchAgentSecretHandlingTest(unittest.TestCase):
    def test_install_script_sources_env_file_without_embedding_api_key(self) -> None:
        module = load_agent_module()
        secret = "unit-test-secret-value"
        agent = module.GoAgentHarnessAgent(
            model="gpt-test",
            openai_api_key=secret,
            openai_base_url="https://example.invalid/v1",
            harness_provider="openai",
        )

        env_path = agent._write_env_file()
        try:
            self.assertIn(secret, env_path.read_text())
            install_script = agent._build_install_script("repo.tar", f"/tmp/{env_path.name}")
        finally:
            env_path.unlink(missing_ok=True)

        self.assertNotIn(secret, install_script)
        self.assertIn(f". /tmp/{env_path.name}", install_script)

    def test_extract_run_id_accepts_harnesscli_json_events(self) -> None:
        module = load_agent_module()
        output = 'run.completed {"id":"event-1","run_id":"run_123","type":"run.completed"}'
        self.assertEqual(module.GoAgentHarnessAgent._extract_run_id(output), "run_123")

    def test_fetch_json_uses_container_exec_output(self) -> None:
        module = load_agent_module()

        class Result:
            exit_code = 0
            output = b'{"run_id":"run_123","status":"completed"}'

        class Container:
            def exec_run(self, args: list[str]) -> Result:
                self.args = args
                return Result()

        class Session:
            container = Container()

        got = module.GoAgentHarnessAgent._fetch_json(Session(), "http://127.0.0.1:8080/summary")

        self.assertEqual(got, {"run_id": "run_123", "status": "completed"})
        self.assertEqual(Session.container.args, ["curl", "-fsS", "http://127.0.0.1:8080/summary"])


if __name__ == "__main__":
    unittest.main()
