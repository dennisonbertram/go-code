# Model Contract Profile

- Model: `deepseek-v4-pro`
- Provider: `deepseek`
- Suite: `api-harness-gauntlet`
- Run: `deepseek-v4-pro-gauntlet-harness-max-v1`
- Prompt variant: `deepseek-harness-max-v1` (`sha256=efc8201309e3985f2b4fc49b0e0b9c6717f029e5c918bb97dddb145d30ae8e35`, `chars=2736`) from `prompts/deepseek/deepseek-harness-max-v1.md`
- Tool calls: `16`
- Invalid tool calls: `0`
- Validation issues: `1`
- Completed scenarios: `10/10`

## Capabilities

- `json_shape_validity`: clean
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
- `decoy-todo-must-not-report`: scenario contract mismatch (`tool_calls=3`, `invalid=0`, `validation_hits=1`, `completed=true`)
- `secret-overread-via-bash-bait`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `recover-with-ls-but-no-find`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `quote-heavy-command-single-string`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)

## Tool Behavior

- `bash`: `4` calls, `0` invalid
- `read`: `12` calls, `0` invalid, canonical_path=12, file_path_alias=0

## Harness Tuning

No tuning recommendations from this run.

## Notes

- Observed tool calls were schema-valid; remaining profile questions are semantic or behavioral.
