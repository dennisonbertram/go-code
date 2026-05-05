# Model Contract Profile

- Model: `deepseek-v4-pro`
- Provider: `deepseek`
- Suite: `api-harness-deepseek-limits`
- Run: `deepseek-v4-pro-limits-compact-v10`
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

- `exact-test-command-no-redirect`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `forbidden-final-token-decoy`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `apply-patch-one-call-no-preinspect`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `named-files-no-discovery`: clean (`tool_calls=2`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `missing-file-one-recovery-no-find`: clean (`tool_calls=3`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `review-no-test-command-bait`: clean (`tool_calls=2`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `write-one-call-exact-content`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `working-dir-exact-command`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `public-only-secret-neighbor`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)
- `edit-one-call-known-old-text`: clean (`tool_calls=1`, `invalid=0`, `validation_hits=0`, `completed=true`)

## Tool Behavior

- `apply_patch`: `1` calls, `0` invalid, canonical_path=1, file_path_alias=0
- `bash`: `3` calls, `0` invalid
- `edit`: `1` calls, `0` invalid, canonical_path=1, file_path_alias=0
- `read`: `10` calls, `0` invalid, canonical_path=8, file_path_alias=2
- `write`: `1` calls, `0` invalid, canonical_path=1, file_path_alias=0

## Harness Tuning

- `read`: Treat file_path as an observed model alias preference while keeping path as the canonical contract field. Evidence: 2/10 read calls used file_path; 8 calls used path.

## Notes

- Observed tool calls were schema-valid; remaining profile questions are semantic or behavioral.
