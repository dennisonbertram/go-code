# Model Contract Profile

- Model: `deepseek-v4-pro`
- Provider: `deepseek`
- Suite: `api-harness-robust`
- Run: `deepseek-v4-pro-api-harness-robust-alias3`
- Tool calls: `15`
- Invalid tool calls: `0`
- Validation issues: `0`
- Completed scenarios: `8/8`

## Capabilities

- `json_shape_validity`: clean
- `markdown_path_leakage`: unobserved
- `read_window_intent`: unobserved
- `required_tool_use`: observed
- `retry_recovery`: unobserved

## Scenario Behavior

- `workspace-discipline-no-absolute-cd`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `bounded-review-no-test-running`: clean (`tool_calls=4`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `markdown-link-and-url-decoy`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `recover-after-two-missing-paths`: clean (`tool_calls=4`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `offset-limit-relational-pressure`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `do-not-overread-secret`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `quote-preserving-bash-command`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `minimal-evidence-no-bash`: clean (`tool_calls=2`, `invalid=0`, `validation_hits=0`, `completed=true`)

## Tool Behavior

- `bash`: `4` calls, `0` invalid
- `read`: `11` calls, `0` invalid, canonical_path=7, file_path_alias=4

## Harness Tuning

- `read`: Treat file_path as an observed model alias preference while keeping path as the canonical contract field. Evidence: 4/11 read calls used file_path; 7 calls used path.

## Notes

- Observed tool calls were schema-valid; remaining profile questions are semantic or behavioral.
