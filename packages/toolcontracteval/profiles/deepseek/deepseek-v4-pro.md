# Model Contract Profile

- Model: `deepseek-v4-pro`
- Provider: `deepseek`
- Suite: `api-harness-production`
- Run: `deepseek-v4-pro-api-harness-production`
- Tool calls: `9`
- Invalid tool calls: `0`
- Validation issues: `0`
- Completed scenarios: `5/5`

## Capabilities

- `json_shape_validity`: clean
- `markdown_path_leakage`: clean
- `read_window_intent`: clean
- `required_tool_use`: observed
- `retry_recovery`: clean

## Scenario Behavior

- `read-first-lines-contract`: clean (`tool_calls=1`, `invalid=0`, `completed=true`)
- `read-bad-path-recovery`: clean (`tool_calls=3`, `invalid=0`, `completed=true`)
- `path-string-no-markdown`: clean (`tool_calls=1`, `invalid=0`, `completed=true`)
- `bash-json-array-command`: clean (`tool_calls=1`, `invalid=0`, `completed=true`)
- `review-multi-tool-workflow`: clean (`tool_calls=3`, `invalid=0`, `completed=true`)

## Tool Behavior

- `bash`: `3` calls, `0` invalid
- `read`: `6` calls, `0` invalid

## Harness Tuning

- `read`: Keep semantic convenience fields such as first_lines visible for this model. Evidence: Read-window scenario completed without validation failures.

## Notes

- Observed tool calls were schema-valid; remaining profile questions are semantic or behavioral.
