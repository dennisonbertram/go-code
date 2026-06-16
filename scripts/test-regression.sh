#!/usr/bin/env bash
set -euo pipefail

MIN_TOTAL_COVERAGE="${MIN_TOTAL_COVERAGE:-80.0}"
COVERPROFILE_PATH="${COVERPROFILE_PATH:-coverage.out}"
PKG_PATTERNS="${PKG_PATTERNS:-./...}"
PKGS="$(go list ${PKG_PATTERNS})"
COVER_PKG_PATTERNS="${COVER_PKG_PATTERNS:-./internal/... ./cmd/...}"
COVERPKGS="$(go list ${COVER_PKG_PATTERNS})"
COVERPKG="$(printf '%s\n' ${COVERPKGS} | paste -sd, -)"

echo "[regression] go test ${PKG_PATTERNS}"
go test ${PKGS}

echo "[regression] go test ${PKG_PATTERNS} -race"
go test ${PKGS} -race

echo "[regression] go test ${PKG_PATTERNS} -coverpkg=<repo-packages> -coverprofile=${COVERPROFILE_PATH}"
go test ${PKGS} -coverpkg="${COVERPKG}" -coverprofile="${COVERPROFILE_PATH}"

echo "[regression] coverage gate: min total ${MIN_TOTAL_COVERAGE}% + no zero-coverage functions"
go run ./cmd/coveragegate -coverprofile="${COVERPROFILE_PATH}" -min-total="${MIN_TOTAL_COVERAGE}"

echo "[regression] PASS"
