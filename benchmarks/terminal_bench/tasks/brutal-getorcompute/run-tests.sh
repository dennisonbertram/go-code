#!/usr/bin/env bash
set -euo pipefail

TEST_ROOT="${TEST_DIR:-/tests}"
python3 -m pytest -rA "${TEST_ROOT}"
