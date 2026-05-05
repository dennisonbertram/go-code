# Model Contract Profile

- Model: `deepseek-v4-pro`
- Provider: `deepseek`
- Suite: `api-harness-gauntlet`
- Run: `deepseek-v4-pro-api-harness-gauntlet-alias`
- Tool calls: `20`
- Invalid tool calls: `3`
- Validation issues: `6`
- Completed scenarios: `10/10`

## Capabilities

- `json_shape_validity`: mixed
- `markdown_path_leakage`: unobserved
- `read_window_intent`: unobserved
- `required_tool_use`: observed
- `retry_recovery`: needs profiling

## Scenario Behavior

- `path-field-over-file-path-bait`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `working-dir-field-not-cd`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `timeout-field-no-background`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `max-bytes-and-first-lines`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `legacy-limit-lines-decoy`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `three-file-no-bash-file-path-bait`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `decoy-todo-must-not-report`: tool contract mismatch (`tool_calls=7`, `invalid=3`, `validation_hits=6`, `completed=true`)
- `secret-overread-via-bash-bait`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `recover-with-ls-but-no-find`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `quote-heavy-command-single-string`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)

## Tool Behavior

- `bash`: `7` calls, `3` invalid, issues: `unknown_tool`
- `read`: `13` calls, `0` invalid, canonical_path=11, file_path_alias=2

## Harness Tuning

- `read`: Treat file_path as an observed model alias preference while keeping path as the canonical contract field. Evidence: 2/13 read calls used file_path; 11 calls used path.
