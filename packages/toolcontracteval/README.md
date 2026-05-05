# Tool Contract Eval Lab

This package is a development-only lab for measuring model/tool-contract fit. Eval runs drive a running `harnessd` over `/v1/runs` and `/v1/runs/{id}/events` so profiles reflect the full harness server path.

It runs scenarios against production harness tools, records raw API/SSE events, invalid tool-call shapes, tool results, scenario assertions, repair simulations, clustered failures, and human-reviewable reports/profiles. Model profiling is API-only by design; the package should not call model providers directly or emulate tool execution with CLI/stub workarounds.

Durable model learnings live in `profiles/<provider>/<model>.{json,md}`. Run-local generated profiles live under `.runs/<run-id>/model-profile.{json,md}`.

## Run Tests

```bash
go test ./...
```

## Run API Evals

Start `harnessd` in tmux first. Configure the model/provider on the server, or pass them per eval run. For DeepSeek:

```bash
tmux new-session -d -s harnessd-eval \
  'cd /Users/dennisonbertram/Develop/go-agent-harness && \
   source ~/.zshrc >/dev/null 2>&1; \
   HARNESS_AUTH_DISABLED=true \
   HARNESS_ADDR=127.0.0.1:49317 \
   HARNESS_MODEL=deepseek-v4-pro \
   HARNESS_MODEL_CATALOG_PATH=/Users/dennisonbertram/Develop/go-agent-harness/catalog/models.json \
   HARNESS_RUN_DB=/Users/dennisonbertram/Develop/go-agent-harness/.tmp/toolcontracteval-runs.db \
   HARNESS_WORKSPACE=/Users/dennisonbertram/Develop/go-agent-harness \
   HARNESS_MAX_STEPS=8 \
   GOCACHE=/Users/dennisonbertram/Develop/go-agent-harness/.tmp/go-build \
   go run ./cmd/harnessd'
```

Then run the production harness suite:

```bash
tmux new-session -d -s eval-deepseek-api \
  'cd /Users/dennisonbertram/Develop/go-agent-harness/packages/toolcontracteval && \
go run ./cmd/toolcontracteval run \
   --api-base-url http://127.0.0.1:49317 \
   --provider deepseek \
   --model deepseek-v4-pro \
   --suite api-harness-production'
```

Suites must use production tools only. `toolcontracteval run` rejects suite-defined fake tools so run artifacts cannot be mistaken for full-harness behavior.

Prompt variants can be evaluated without changing the production prompt:

```bash
go run ./cmd/toolcontracteval run \
  --api-base-url http://127.0.0.1:49317 \
  --provider deepseek \
  --model deepseek-v4-pro \
  --suite api-harness-deepseek-hard \
  --run-id deepseek-v4-pro-hard-harness-max-v1 \
  --system-prompt-file prompts/deepseek/deepseek-harness-max.md \
  --system-prompt-label deepseek-harness-max-v1
```

The prompt text is copied into `system-prompt.md`, and the manifest records the prompt label, path, SHA-256, and character count.

Regenerate reports or replay repairs:

```bash
go run ./cmd/toolcontracteval report --run .runs/<run-id>
go run ./cmd/toolcontracteval profile --run .runs/<run-id>
go run ./cmd/toolcontracteval profile --run .runs/<run-id> --profiles-dir profiles
go run ./cmd/toolcontracteval replay --run .runs/<run-id>
```

Clean prompt-variant runs also include a candidate runtime prompt profile in
`model-profile.{json,md}`. Generated profiles are descriptive only; the live
harness does not auto-load them. After reviewing the candidate, promote it
manually into the runtime prompt catalog:

```bash
go run ./cmd/toolcontracteval promote-profile \
  --run .runs/<run-id> \
  --prompts-dir ../../prompts \
  --profile-name deepseek \
  --match 'deepseek-*' \
  --dry-run

go run ./cmd/toolcontracteval promote-profile \
  --run .runs/<run-id> \
  --prompts-dir ../../prompts \
  --profile-name deepseek \
  --match 'deepseek-*'
```

Promotion is intentionally gated: without `--force`, the run must complete all
scenarios with zero invalid tool calls and zero validation hits.

## Output

Each run writes:

```text
.runs/<run-id>/
  manifest.json
  tool-definitions.json
  scenario-results.jsonl
  tool-calls.jsonl
  api-events.jsonl
  validation-failures.jsonl
  retry-messages.jsonl
  tool-results.jsonl
  repair-simulation.jsonl
  clusters.json
  report.md
  model-profile.json
  model-profile.md
```

## Durable Profiles

When a run produces a useful model learning, promote it into durable profiles with:

```bash
go run ./cmd/toolcontracteval profile --run .runs/<run-id> --profiles-dir profiles
```

That writes:

```text
profiles/<provider>/<model>.json
profiles/<provider>/<model>.md
```

These profiles are descriptive. They do not automatically change harness behavior; they are inputs for future routing, prompting, schema, and repair decisions.
