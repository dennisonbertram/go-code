# Model Contract Profile

- Model: `deepseek-v4-pro`
- Provider: `deepseek`
- Suite: `api-harness-gauntlet`
- Run: `deepseek-v4-pro-gauntlet-compact-v10`
- Prompt variant: `deepseek-harness-compact-v10` (`sha256=bc02f080ace96894c5c452aef83f01fa0f8b6569bca278004ce458e8c6cb1358`, `chars=1121`) from `prompts/deepseek/deepseek-harness-compact-v10.md`
- Tool calls: `16`
- Invalid tool calls: `0`
- Validation issues: `0`
- Completed scenarios: `10/10`

## Capabilities

- `json_shape_validity`: clean
- `markdown_path_leakage`: unobserved
- `read_window_intent`: unobserved
- `required_tool_use`: observed
- `retry_recovery`: unobserved

## Scenario Behavior

- `path-field-over-file-path-bait`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `working-dir-field-not-cd`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `timeout-field-no-background`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `max-bytes-and-first-lines`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `legacy-limit-lines-decoy`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `three-file-no-bash-file-path-bait`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `decoy-todo-must-not-report`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `secret-overread-via-bash-bait`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `recover-with-ls-but-no-find`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `quote-heavy-command-single-string`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)

## Tool Behavior

- `bash`: `4` calls, `0` invalid
- `read`: `12` calls, `0` invalid, canonical_path=11, file_path_alias=1

## Harness Tuning

- `read`: Treat file_path as an observed model alias preference while keeping path as the canonical contract field. Evidence: 1/12 read calls used file_path; 11 calls used path.

## Notes

- Observed tool calls were schema-valid; remaining profile questions are semantic or behavioral.
