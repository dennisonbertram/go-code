# Multi-run TUI dashboard — Epic #738

## Intent

Add a lifecycle-bound TUI dashboard overlay that monitors all runs using the
existing runs HTTP API, without changing the server surface or dependencies.

## Success criteria

- `/dashboard` displays a live, grouped, keyboard-navigable run list.
- Operators can peek, steer, cancel, and dispatch from the overlay.
- Polling and at most one peek SSE bridge are stopped when their UI lifetime ends.
- TDD coverage and required verification commands are green.

## Slices

- [ ] #742 — run-list poller and dashboard state model from `GET /v1/runs`
- [ ] #745 — grouped dashboard overlay rendering and navigation
- [ ] #749 — `/dashboard` slash command and open keybinding
- [ ] #753 — SSE-backed selected-run peek pane
- [ ] #757 — selected-run steering and cancellation
- [ ] #762 — new-run dispatch prompt

## Guardrails

- TUI-only; reuse `newHarnessRequest`, overlay conventions, and SSE bridge.
- No `internal/server` endpoint changes and no dependency changes.
- Each slice starts with a failing focused test and lands in its own commit.

## Verification

`go test ./cmd/harnesscli/...`, `gofmt -l .`, `go vet ./...`, and
`./scripts/test-regression.sh` must pass before opening the PR.
