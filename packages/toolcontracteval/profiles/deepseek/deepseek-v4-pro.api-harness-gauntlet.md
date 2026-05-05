# Model Contract Profile

- Model: `deepseek-v4-pro`
- Provider: `deepseek`
- Suite: `api-harness-gauntlet`
- Run: `deepseek-v4-pro-api-harness-gauntlet`
- Tool calls: `16`
- Invalid tool calls: `1`
- Validation issues: `2`
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
- `max-bytes-and-first-lines`: tool contract mismatch (`tool_calls=1`, `invalid=1`, `validation_hits=1`, `completed=true`)
- `legacy-limit-lines-decoy`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `three-file-no-bash-file-path-bait`: scenario contract mismatch (`tool_calls=3`, `invalid=0`, `validation_hits=1`, `completed=true`)
- `decoy-todo-must-not-report`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `secret-overread-via-bash-bait`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `recover-with-ls-but-no-find`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `quote-heavy-command-single-string`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)

## Tool Behavior

- `bash`: `4` calls, `0` invalid
- `read`: `12` calls, `1` invalid, issues: `required`

## Harness Tuning

- `read`: Consider accepting file_path as a safe alias for path, or strengthen the read schema description for this model. Evidence: 1 read calls used file_path where path was required.

## Gauntlet Notes

- DeepSeek resisted explicit `file_path` bait when the prompt directly contrasted `path` and `file_path`.
- The remaining `file_path` drift appeared when `read` also had `first_lines` and `max_bytes`, suggesting alias drift may increase under multi-field read calls.
- The scenario-level miss was not a tool failure: the model identified the bug correctly but over-explained by mentioning `AddOne`, a non-bug file that the scenario treated as a forbidden decoy.
