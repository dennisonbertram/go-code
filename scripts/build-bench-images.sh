#!/usr/bin/env bash
# build-bench-images.sh — Build all terminal-bench Docker images.
# Builds a shared base image once, then lightweight per-task images on top.
# Safe to run repeatedly (cache-friendly). Used by run-terminal-bench.sh and CI.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BENCH_DIR="${REPO_ROOT}/benchmarks/terminal_bench"
DATASET_PATH="${BENCH_DIR}/tasks"

BASE_IMAGE="go-agent-harness-tb-base:latest"
DOCKER_BUILDKIT_VALUE="${DOCKER_BUILDKIT:-1}"

echo "[build-bench-images] Building base image: ${BASE_IMAGE}"
DOCKER_BUILDKIT="${DOCKER_BUILDKIT_VALUE}" docker build \
  --pull=false \
  -t "${BASE_IMAGE}" \
  -f "${BENCH_DIR}/Dockerfile.base" \
  "${BENCH_DIR}"

TASKS=(
  "go-retry-schedule-fix:go-agent-harness-tb-go-retry"
  "staging-deploy-docs:go-agent-harness-tb-staging-docs"
  "incident-summary-shell:go-agent-harness-tb-incident-shell"
  "go-race-condition-fix:go-agent-harness-tb-go-race"
  "go-rename-refactor:go-agent-harness-tb-go-rename"
  "multi-report-pipeline:go-agent-harness-tb-multi-report"
  "go-interface-migration:go-agent-harness-tb-go-interface"
)

for entry in "${TASKS[@]}"; do
  task_dir="${entry%%:*}"
  image_name="${entry##*:}"
  echo "[build-bench-images] Building task image: ${image_name}:latest"
  DOCKER_BUILDKIT="${DOCKER_BUILDKIT_VALUE}" docker build \
    --pull=false \
    -t "${image_name}:latest" \
    "${DATASET_PATH}/${task_dir}"
done

echo "[build-bench-images] All images built successfully."
docker images --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}" | grep go-agent-harness-tb || true
