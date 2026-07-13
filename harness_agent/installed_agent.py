"""
HarnessInstalledAgent — Harbor BaseAgent adapter that runs harnessd inside
the Harbor container.

Tests our full harness: all 40+ tools, our system prompt, our agent loop.
The model is just the backend — harnessd orchestrates everything.
"""

from __future__ import annotations

import asyncio
import os
import shlex
from pathlib import Path

from harbor.agents.base import BaseAgent
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext


class HarnessInstalledAgent(BaseAgent):
    """
    Runs harnessd inside the Harbor container.
    Tests our full harness: all 40+ tools, our system prompt, our agent loop.
    The model is just the backend — harnessd orchestrates everything.
    """

    SUPPORTS_ATIF = False
    BINARY_DIR = Path(__file__).parent / "bin"

    @staticmethod
    def name() -> str:
        return "harness-installed"

    def version(self) -> str | None:
        return "0.1.0"

    async def setup(self, environment: BaseEnvironment) -> None:
        # Detect container architecture
        arch_result = await environment.exec("uname -m")
        container_arch = (arch_result.stdout or "").strip()
        self.logger.info("Container architecture: %s", container_arch)

        if "aarch64" in container_arch or "arm64" in container_arch:
            suffix = "linux-arm64"
        else:
            suffix = "linux-amd64"

        harnessd = self.BINARY_DIR / f"harnessd-{suffix}"
        harnesscli = self.BINARY_DIR / f"harnesscli-{suffix}"
        if not harnessd.exists() or not harnesscli.exists():
            raise FileNotFoundError(
                f"Binary {harnessd} not found. Run ./harness_agent/build_binaries.sh first."
            )
        prompts_dir = self.BINARY_DIR.parent.parent / "prompts"

        self.logger.info("Uploading harnessd and harnesscli (%s) to container...", suffix)
        await environment.exec("mkdir -p /harness-agent/rollouts /harness-agent/prompts")
        await environment.upload_file(harnessd, "/harness-agent/harnessd")
        await environment.upload_file(harnesscli, "/harness-agent/harnesscli")
        await environment.exec("chmod +x /harness-agent/harnessd /harness-agent/harnesscli")

        # harnessd needs prompts/ directory with catalog.yaml
        self.logger.info("Uploading prompts directory...")
        await environment.upload_dir(prompts_dir, "/harness-agent/prompts")

        # Upload model catalog so harnessd can resolve provider from model name
        catalog_path = self.BINARY_DIR.parent.parent / "catalog" / "models.json"
        if catalog_path.exists():
            await environment.exec("mkdir -p /harness-agent/catalog")
            await environment.upload_file(catalog_path, "/harness-agent/catalog/models.json")
            self.logger.info("Uploaded model catalog to container.")

        # Upload host CA bundle so Go TLS can verify external API certs in containers
        # that may not have ca-certificates installed (e.g., ubuntu:24.04 minimal images)
        for ca_path in ["/etc/ssl/cert.pem", "/etc/ssl/certs/ca-certificates.crt"]:
            import pathlib
            if pathlib.Path(ca_path).exists():
                await environment.upload_file(pathlib.Path(ca_path), "/harness-agent/ca-bundle.pem")
                self.logger.info("Uploaded CA bundle from %s", ca_path)
                break

        self.logger.info("Harness binaries and prompts installed in container.")

    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        # Parse provider/model from harbor's "provider/model" format
        raw = self.model_name or "openai/gpt-4.1-mini"
        model = raw.split("/", 1)[-1] if "/" in raw else raw

        api_key = os.environ.get("OPENAI_API_KEY", "")
        anthropic_key = os.environ.get("ANTHROPIC_API_KEY", "")
        google_key = os.environ.get("GOOGLE_API_KEY", "")

        env = {
            "OPENAI_API_KEY": api_key,
            "ANTHROPIC_API_KEY": anthropic_key,
            "GOOGLE_API_KEY": google_key,
            "HARNESS_MODEL": model,
            # The benchmark runs headless in a container: opt into the
            # autonomous overlay (no user present, act and verify, /app task
            # framing). harnessd's default is now the neutral base prompt.
            "HARNESS_DEFAULT_AGENT_INTENT": "autonomous",
            "HARNESS_MAX_STEPS": "50",
            "HARNESS_PROMPTS_DIR": "/harness-agent/prompts",
            "HARNESS_ROLLOUT_DIR": "/harness-agent/rollouts",
            "HARNESS_MODEL_CATALOG_PATH": "/harness-agent/catalog/models.json",
            "HARNESS_ADDR": ":8080",
            # SSL_CERT_FILE tells Go TLS where to find trusted CA certs.
            # We upload the host's CA bundle in setup() to /harness-agent/ca-bundle.pem.
            "SSL_CERT_FILE": "/harness-agent/ca-bundle.pem",
        }

        # Single shell script: start harnessd, wait for ready, run task, collect result
        script = f"""#!/bin/bash
set -eo pipefail

# Capture container's WORKDIR before cd-ing to /harness-agent
# Different task Docker images use different WORKDIRs (e.g. /app/personal-site for fix-git)
CONTAINER_WORKDIR=$(pwd)
export HARNESS_WORKSPACE="$CONTAINER_WORKDIR"
echo "[harness] container WORKDIR: $CONTAINER_WORKDIR"

cd /harness-agent

echo "[harness] starting harnessd (model={model}, max_steps=$HARNESS_MAX_STEPS)..."
./harnessd >> /harness-agent/harnessd.log 2>&1 &
HARNESS_PID=$!
trap 'kill $HARNESS_PID 2>/dev/null || true' EXIT

# Wait up to 60s for harnessd to be ready
echo "[harness] waiting for harnessd on :8080..."
READY=0
for i in $(seq 1 30); do
    # /dev/tcp works in bash without any external tools (critical for ubuntu:24.04)
    if (echo >/dev/tcp/127.0.0.1/8080) 2>/dev/null; then
        READY=1; break
    elif curl -sf http://127.0.0.1:8080/healthz > /dev/null 2>&1; then
        READY=1; break
    elif wget -qO- http://127.0.0.1:8080/healthz > /dev/null 2>&1; then
        READY=1; break
    elif python3 -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8080/healthz')" > /dev/null 2>&1; then
        READY=1; break
    fi
    sleep 2
    if [ $i -eq 30 ]; then
        echo "[harness] ERROR: harnessd did not start within 60s"
        echo "[harness] harnessd log:"
        cat /harness-agent/harnessd.log
        exit 1
    fi
done
echo "[harness] harnessd ready after $((i*2))s"

# Run the task — harnesscli blocks until done, outputs SSE stream
echo "[harness] submitting task..."
./harnesscli \\
    -base-url=http://127.0.0.1:8080 \\
    -model="{model}" \\
    -prompt={shlex.quote(instruction)} \\
    2>&1

echo "[harness] task complete"
"""

        self.logger.info("HarnessInstalledAgent running: model=%s", model)
        result = await environment.exec(command=script, env=env, timeout_sec=1800)

        stdout = result.stdout or ""
        self.logger.info(
            "Exit code: %d, output length: %d chars",
            result.return_code,
            len(stdout),
        )

        # Always log full output for debugging
        if stdout:
            self.logger.info("Output:\n%s", stdout[:8000])
        stderr = getattr(result, "stderr", None) or ""
        if stderr:
            self.logger.info("Stderr:\n%s", stderr[:2000])

        if result.return_code != 0:
            # Log harnessd log if available
            try:
                log_result = await environment.exec("cat /harness-agent/harnessd.log 2>/dev/null | tail -50")
                if log_result.stdout:
                    self.logger.info("harnessd log:\n%s", log_result.stdout)
            except Exception:
                pass

        # Parse terminal_event from harnesscli output
        for line in stdout.splitlines():
            if line.startswith("terminal_event="):
                self.logger.info("Result: %s", line)
            elif line.startswith("run_id="):
                self.logger.info("Run ID: %s", line)
