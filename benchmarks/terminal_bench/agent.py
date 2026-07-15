from __future__ import annotations

import json
import os
import platform
import re
import shlex
import subprocess
import tarfile
import tempfile
import threading
from pathlib import Path
from datetime import datetime, timezone

from terminal_bench.agents.base_agent import AgentResult, BaseAgent
from terminal_bench.agents.failure_mode import FailureMode
from terminal_bench.terminal.tmux_session import TmuxSession

REPO_ROOT = Path(__file__).resolve().parents[2]
CONTAINER_REPO_ROOT = "/opt/go-agent-harness"
CONTAINER_BIN_DIR = "/tmp/go-agent-harness-bin"
HARNESS_BASE_URL = "http://127.0.0.1:8080"
_BINARY_CACHE: dict[str, Path] = {}
_BINARY_CACHE_LOCK = threading.Lock()


class GoAgentHarnessAgent(BaseAgent):
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self._model = kwargs.get("model", os.getenv("HARNESS_BENCH_MODEL", "gpt-5-mini"))
        self._api_key = kwargs.get("openai_api_key", os.getenv("OPENAI_API_KEY", ""))
        self._base_url = kwargs.get("openai_base_url", os.getenv("OPENAI_BASE_URL", ""))
        self._max_steps = kwargs.get("harness_max_steps", os.getenv("HARNESS_BENCH_MAX_STEPS", "100"))
        self._memory_mode = kwargs.get("harness_memory_mode", os.getenv("HARNESS_BENCH_MEMORY_MODE", "off"))
        self._target_arch = kwargs.get("target_arch", os.getenv("HARNESS_BENCH_TARGET_ARCH", self._default_target_arch()))
        self._provider = kwargs.get("harness_provider", os.getenv("HARNESS_PROVIDER", ""))
        self._fake_turns = kwargs.get("harness_fake_turns", os.getenv("HARNESS_FAKE_TURNS", ""))
        self._binary_dir = kwargs.get("harness_binary_dir", os.getenv("HARNESS_BENCH_BINARY_DIR", ""))
        self._pricing_catalog_path = kwargs.get(
            "harness_pricing_catalog_path",
            os.getenv(
                "HARNESS_BENCH_PRICING_CATALOG_PATH",
                f"{CONTAINER_REPO_ROOT}/catalog/pricing.json",
            ),
        )

    @staticmethod
    def name() -> str:
        return "go-agent-harness"

    def perform_task(
        self,
        instruction: str,
        session: TmuxSession,
        logging_dir: Path | None = None,
    ) -> AgentResult:
        fake_mode = self._provider == "fake" and bool(self._fake_turns)
        if not self._api_key and not fake_mode:
            return AgentResult(failure_mode=FailureMode.AGENT_INSTALLATION_FAILED)

        archive_path = self._package_repo()
        env_file_path: Path | None = None
        binary_dir = self._build_binaries()
        container_fake_turns = ""
        session.copy_to_container(paths=[archive_path], container_dir="/tmp")
        if self._fake_turns:
            fake_turns_path = Path(self._fake_turns)
            session.copy_to_container(paths=[fake_turns_path], container_dir="/tmp")
            container_fake_turns = f"/tmp/{fake_turns_path.name}"
        env_file_path = self._write_env_file(container_fake_turns)
        session.copy_to_container(paths=[env_file_path], container_dir="/tmp")
        container_env_path = f"/tmp/{env_file_path.name}"
        session.copy_to_container(
            paths=[binary_dir / "harnessd", binary_dir / "harnesscli"],
            container_dir="/tmp",
        )

        try:
            install_script = self._build_install_script(archive_path.name, container_env_path)
            session.send_keys([f"bash -lc {shlex.quote(install_script)}", "Enter"], block=True, max_timeout_sec=1200)

            run_script = self._build_run_script(self._render_instruction(instruction))
            session.clear_history()
            session.send_keys([f"bash -lc {shlex.quote(run_script)}", "Enter"], block=True, max_timeout_sec=1800)

            terminal_output = session.capture_pane(capture_entire=True)
            run_id = self._extract_run_id(terminal_output)
            run_record = self._fetch_run_record(session, run_id)
            telemetry = self._fetch_telemetry(session, run_id)
            if logging_dir:
                logging_dir.mkdir(parents=True, exist_ok=True)
                if telemetry:
                    (logging_dir / "harness_telemetry.json").write_text(
                        json.dumps(telemetry, indent=2)
                    )
                benchmark_result = self._build_benchmark_result(
                    run_record,
                    telemetry,
                    self._render_instruction(instruction),
                    logging_dir,
                )
                if benchmark_result:
                    (logging_dir / "benchmark_result.json").write_text(
                        json.dumps(benchmark_result, indent=2)
                    )
                harness_log = self._capture_container_file(session, "/tmp/harnessd.log")
                if harness_log:
                    (logging_dir / "harnessd.log").write_text(harness_log)
            if "terminal_event=run.completed" in terminal_output:
                return self._make_result(FailureMode.NONE, telemetry)
            return AgentResult(failure_mode=FailureMode.UNKNOWN_AGENT_ERROR)
        finally:
            archive_path.unlink(missing_ok=True)
            if env_file_path:
                env_file_path.unlink(missing_ok=True)

    def _build_install_script(self, archive_name: str, container_env_path: str) -> str:
        quoted_env_path = shlex.quote(container_env_path)
        tmux_command = (
            f'cd "$TASK_ROOT" && set -a && . {quoted_env_path} && set +a && '
            f'HARNESS_WORKSPACE="$TASK_ROOT" {CONTAINER_BIN_DIR}/harnessd >/tmp/harnessd.log 2>&1'
        )
        return f"""
set -euo pipefail
TASK_ROOT="$(pwd)"
mkdir -p {CONTAINER_BIN_DIR}
rm -rf {CONTAINER_REPO_ROOT}
mkdir -p {CONTAINER_REPO_ROOT}
tar -xf /tmp/{archive_name} -C {CONTAINER_REPO_ROOT} --strip-components=1
mv /tmp/harnessd {CONTAINER_BIN_DIR}/harnessd
mv /tmp/harnesscli {CONTAINER_BIN_DIR}/harnesscli
chmod +x {CONTAINER_BIN_DIR}/harnessd {CONTAINER_BIN_DIR}/harnesscli
cd {CONTAINER_REPO_ROOT}
tmux kill-session -t harnessd >/dev/null 2>&1 || true
tmux new-session -d -s harnessd "{tmux_command}"
for attempt in $(seq 1 90); do
  if curl -fsS {HARNESS_BASE_URL}/healthz >/dev/null 2>&1; then
    exit 0
  fi
  sleep 1
done
echo "harness server did not become healthy" >&2
tail -n 200 /tmp/harnessd.log >&2 || true
exit 1
"""

    def _write_env_file(self, container_fake_turns: str = "") -> Path:
        fd, temp_path = tempfile.mkstemp(prefix="go-agent-harness-env-", suffix=".sh")
        os.close(fd)
        env_path = Path(temp_path)
        env_map = {
            "OPENAI_API_KEY": self._api_key,
            "OPENAI_BASE_URL": self._base_url,
            "HARNESS_PROVIDER": self._provider,
            "HARNESS_FAKE_TURNS": container_fake_turns,
            "HARNESS_ADDR": ":8080",
            "HARNESS_MODEL": self._model,
            "HARNESS_MAX_STEPS": str(self._max_steps),
            "HARNESS_MEMORY_MODE": self._memory_mode,
            "HARNESS_PROMPTS_DIR": f"{CONTAINER_REPO_ROOT}/prompts",
            "HARNESS_PRICING_CATALOG_PATH": self._pricing_catalog_path,
            # Widen provider retry budget inside the container against rate-limited
            # (e.g. free-tier) endpoints. Passed through from the host env; unset
            # leaves the harness built-in defaults (3 attempts / 60s).
            "HARNESS_RETRY_MAX_ATTEMPTS": os.getenv("HARNESS_RETRY_MAX_ATTEMPTS", ""),
            "HARNESS_RETRY_MAX_TOTAL_SEC": os.getenv("HARNESS_RETRY_MAX_TOTAL_SEC", ""),
        }
        lines = ["# go-code Terminal-Bench harness environment"]
        for key, value in env_map.items():
            if value == "":
                continue
            lines.append(f"{key}={shlex.quote(str(value))}")
        env_path.write_text("\n".join(lines) + "\n")
        env_path.chmod(0o600)
        return env_path

    def _build_run_script(self, instruction: str) -> str:
        cli_command = self._shell_join(
            {},
            (
                f'{CONTAINER_BIN_DIR}/harnesscli '
                f'-base-url={HARNESS_BASE_URL} '
                f'-model={shlex.quote(self._model)} '
                f'-agent-intent=general '
                f'-task-context={shlex.quote("Terminal Bench private smoke suite")} '
                f'-prompt={shlex.quote(instruction)}'
            ),
        )
        return f"""
set -euo pipefail
TASK_ROOT="$(pwd)"
cd "$TASK_ROOT"
{cli_command}
"""

    def _shell_join(self, env_map: dict[str, str], command: str) -> str:
        env_parts = []
        for key, value in env_map.items():
            if value == "":
                continue
            env_parts.append(f"{key}={shlex.quote(value)}")
        if env_parts:
            return " ".join(env_parts) + " " + command
        return command

    def _package_repo(self) -> Path:
        fd, temp_path = tempfile.mkstemp(prefix="go-agent-harness-", suffix=".tar")
        os.close(fd)
        archive_path = Path(temp_path)
        with tarfile.open(archive_path, "w") as archive:
            archive.add(REPO_ROOT, arcname="go-agent-harness", filter=self._tar_filter)
        return archive_path

    def _tar_filter(self, tarinfo: tarfile.TarInfo) -> tarfile.TarInfo | None:
        path = Path(tarinfo.name)
        parts = path.parts[1:]
        if parts:
            root_entry = parts[0]
            if root_entry in {".git", ".tmp", "node_modules"}:
                return None
        return tarinfo

    def _build_binaries(self) -> Path:
        if self._binary_dir:
            candidate = Path(self._binary_dir)
            if (candidate / "harnessd").exists() and (candidate / "harnesscli").exists():
                return candidate
            raise FileNotFoundError(f"HARNESS_BENCH_BINARY_DIR missing harness binaries: {candidate}")

        cache_key = self._target_arch
        with _BINARY_CACHE_LOCK:
            cached = _BINARY_CACHE.get(cache_key)
            if cached and (cached / "harnessd").exists() and (cached / "harnesscli").exists():
                return cached

        temp_dir = Path(tempfile.mkdtemp(prefix="go-agent-harness-bin-"))
        build_env = os.environ.copy()
        build_env.update(
            {
                "GOOS": "linux",
                "GOARCH": self._target_arch,
                "CGO_ENABLED": "0",
            }
        )
        subprocess.run(
            ["go", "build", "-o", str(temp_dir / "harnessd"), "./cmd/harnessd"],
            cwd=REPO_ROOT,
            env=build_env,
            check=True,
        )
        subprocess.run(
            ["go", "build", "-o", str(temp_dir / "harnesscli"), "./cmd/harnesscli"],
            cwd=REPO_ROOT,
            env=build_env,
            check=True,
        )
        with _BINARY_CACHE_LOCK:
            _BINARY_CACHE[cache_key] = temp_dir
        return temp_dir

    def _default_target_arch(self) -> str:
        machine = platform.machine().lower()
        if machine in {"arm64", "aarch64"}:
            return "arm64"
        return "amd64"

    @staticmethod
    def _make_result(failure_mode: FailureMode, telemetry: dict | None) -> AgentResult:
        kwargs: dict = {"failure_mode": failure_mode}
        if telemetry:
            kwargs["total_input_tokens"] = telemetry.get("total_prompt_tokens", 0)
            kwargs["total_output_tokens"] = telemetry.get("total_completion_tokens", 0)
        try:
            return AgentResult(**kwargs)
        except TypeError:
            return AgentResult(failure_mode=failure_mode)

    @staticmethod
    def _extract_run_id(terminal_output: str) -> str | None:
        for pattern in [r"run_id=(\S+)", r'"run_id"\s*:\s*"([^"]+)"']:
            match = re.search(pattern, terminal_output)
            if match:
                return match.group(1)
        return None

    @staticmethod
    def _fetch_telemetry(session: TmuxSession, run_id: str | None) -> dict | None:
        return GoAgentHarnessAgent._fetch_json(session, f"{HARNESS_BASE_URL}/v1/runs/{run_id}/summary" if run_id else "")

    @staticmethod
    def _fetch_run_record(session: TmuxSession, run_id: str | None) -> dict | None:
        return GoAgentHarnessAgent._fetch_json(session, f"{HARNESS_BASE_URL}/v1/runs/{run_id}" if run_id else "")

    @staticmethod
    def _fetch_json(session: TmuxSession, url: str) -> dict | None:
        if not url:
            return None
        try:
            result = session.container.exec_run(["curl", "-fsS", url])
            if result.exit_code != 0:
                return None
            return json.loads(result.output.decode(errors="replace"))
        except Exception:
            return None

    @staticmethod
    def _capture_container_file(session: TmuxSession, path: str) -> str:
        try:
            script = f"test -f {shlex.quote(path)} && cat {shlex.quote(path)} || true"
            result = session.container.exec_run(["sh", "-lc", script])
            if result.exit_code != 0:
                return ""
            return result.output.decode(errors="replace")
        except Exception:
            return ""

    def _build_benchmark_result(
        self,
        run_record: dict | None,
        telemetry: dict | None,
        prompt: str,
        logging_dir: Path,
    ) -> dict | None:
        if not run_record and not telemetry:
            return None
        run_record = run_record or {}
        telemetry = telemetry or {}
        created_at = run_record.get("created_at") or self._now_iso()
        updated_at = run_record.get("updated_at") or created_at
        result = {
            "tool_id": "go-code",
            "task_id": self._task_id_from_logging_dir(logging_dir),
            "run_id": telemetry.get("run_id") or run_record.get("id") or run_record.get("run_id") or "",
            "status": telemetry.get("status") or run_record.get("status") or "failed",
            "steps_taken": int(telemetry.get("steps_taken") or 0),
            "total_prompt_tokens": int(telemetry.get("total_prompt_tokens") or 0),
            "total_completion_tokens": int(telemetry.get("total_completion_tokens") or 0),
            "total_cost_usd": float(telemetry.get("total_cost_usd") or 0.0),
            "cost_status": telemetry.get("cost_status") or "provider_unreported",
            "cache_hit_rate": float(telemetry.get("cache_hit_rate") or 0.0),
            "model": run_record.get("model") or self._model,
            "prompt": run_record.get("prompt") or prompt,
            "created_at": created_at,
            "updated_at": updated_at,
            "duration_ms": self._duration_ms(created_at, updated_at),
            "tool_calls": telemetry.get("tool_calls") or [],
        }
        optional_map = {
            "provider_name": run_record.get("provider_name"),
            "output": run_record.get("output"),
            "tenant_id": run_record.get("tenant_id"),
            "conversation_id": run_record.get("conversation_id"),
            "agent_id": run_record.get("agent_id"),
            "error_message": telemetry.get("error") or run_record.get("error"),
        }
        for key, value in optional_map.items():
            if value:
                result[key] = value
        return result

    @staticmethod
    def _task_id_from_logging_dir(logging_dir: Path) -> str:
        parts = logging_dir.parts
        if len(parts) >= 3 and parts[-1] == "agent-logs":
            return parts[-3]
        if len(parts) >= 2:
            return parts[-2]
        return ""

    @staticmethod
    def _duration_ms(created_at: str, updated_at: str) -> int:
        try:
            start = GoAgentHarnessAgent._parse_ts(created_at)
            end = GoAgentHarnessAgent._parse_ts(updated_at)
            if start and end:
                return int((end - start).total_seconds() * 1000)
        except Exception:
            pass
        return 0

    @staticmethod
    def _parse_ts(value: str) -> datetime | None:
        if not value:
            return None
        normalized = value.replace("Z", "+00:00")
        try:
            return datetime.fromisoformat(normalized)
        except ValueError:
            return None

    @staticmethod
    def _now_iso() -> str:
        return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
