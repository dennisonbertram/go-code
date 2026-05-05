# Model Contract Profile

- Model: `deepseek-v4-pro`
- Provider: `deepseek`
- Suite: `api-harness-robust`
- Run: `deepseek-v4-pro-api-harness-robust`
- Tool calls: `15`
- Invalid tool calls: `2`
- Validation issues: `2`
- Completed scenarios: `8/8`

## Capabilities

- `json_shape_validity`: mixed
- `markdown_path_leakage`: unobserved
- `read_window_intent`: unobserved
- `required_tool_use`: observed
- `retry_recovery`: needs profiling

## Scenario Behavior

- `workspace-discipline-no-absolute-cd`: clean (`tool_calls=1`, `invalid=0`, `completed=true`)
- `bounded-review-no-test-running`: clean (`tool_calls=4`, `invalid=0`, `completed=true`)
- `markdown-link-and-url-decoy`: clean (`tool_calls=1`, `invalid=0`, `completed=true`)
- `recover-after-two-missing-paths`: clean (`tool_calls=4`, `invalid=0`, `completed=true`)
- `offset-limit-relational-pressure`: clean (`tool_calls=1`, `invalid=0`, `completed=true`)
- `do-not-overread-secret`: clean (`tool_calls=1`, `invalid=0`, `completed=true`)
- `quote-preserving-bash-command`: clean (`tool_calls=1`, `invalid=0`, `completed=true`)
- `minimal-evidence-no-bash`: tool contract mismatch (`tool_calls=2`, `invalid=2`, `completed=true`)

## Tool Behavior

- `bash`: `4` calls, `0` invalid
- `read`: `11` calls, `2` invalid, issues: `required`

## Harness Tuning

- `read`: Consider accepting file_path as a safe alias for path, or strengthen the read schema description for this model. Evidence: 2 read calls used file_path where path was required.

## Model Expectation Hypothesis

The observed `file_path` calls suggest a broader schema-dialect expectation: models may have seen file-reading tools more often with `file_path` than `path`. Do not change the harness contract from this single run yet, but treat `read.path` versus `read.file_path` as an active contract-design question for profile-guided harness tuning.
