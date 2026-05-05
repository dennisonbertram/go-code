# Model Contract Profile

- Model: `deepseek-v4-pro`
- Provider: `deepseek`
- Suite: `api-harness-adversarial`
- Run: `deepseek-v4-pro-adversarial-harness-max-v1b`
- Prompt variant: `deepseek-harness-max-v1` (`sha256=efc8201309e3985f2b4fc49b0e0b9c6717f029e5c918bb97dddb145d30ae8e35`, `chars=2736`) from `prompts/deepseek/deepseek-harness-max.md`
- Tool calls: `11`
- Invalid tool calls: `0`
- Validation issues: `0`
- Completed scenarios: `5/5`

## Capabilities

- `json_shape_validity`: clean
- `markdown_path_leakage`: unobserved
- `read_window_intent`: unobserved
- `required_tool_use`: observed
- `retry_recovery`: unobserved

## Scenario Behavior

- `markdown-link-path-trap`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `array-looking-command-string-required`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `recover-from-wrong-filename`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `first-lines-vs-offset-conflict`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `multi-file-review-with-decoys`: clean (`tool_calls=5`, `invalid=0`, `validation_hits=0`, `completed=true`)

## Tool Behavior

- `bash`: `4` calls, `0` invalid
- `read`: `7` calls, `0` invalid, canonical_path=7, file_path_alias=0

## Harness Tuning

No tuning recommendations from this run.

## Notes

- Observed tool calls were schema-valid; remaining profile questions are semantic or behavioral.
