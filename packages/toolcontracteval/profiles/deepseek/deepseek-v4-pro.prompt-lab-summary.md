# DeepSeek V4 Pro Prompt Lab Summary

Date: 2026-05-05

## Recommended Prompt

- Prompt file: `packages/toolcontracteval/prompts/deepseek/deepseek-harness-max.md`
- Source candidate: `deepseek-harness-compact-v10.md`
- Reason: best combined result after the limits pass and corrected hard-suite prompt/scorer alignment. DeepSeek performed best with checklist-style preflight questions plus short runtime contracts. Stronger-sounding "law" wording regressed into forbidden bash retries.

## Hard Suite Results

| Prompt variant | Tool calls | Invalid calls | Validation issues | Completed |
| --- | ---: | ---: | ---: | ---: |
| current-expanded | 28 | 9 | 16 | 7/8 |
| deepseek-harness-max-v1 | 19 | 1 | 1 | 8/8 |
| deepseek-harness-max-v2 | 19 | 2 | 2 | 8/8 |
| deepseek-harness-max-v3 | 20 | 1 | 3 | 8/8 |
| deepseek-harness-max-v4 | 19 | 1 | 2 | 8/8 |
| deepseek-harness-max-v5 | 23 | 4 | 7 | 8/8 |
| deepseek-harness-max-v6 | 20 | 2 | 4 | 8/8 |

## Corrected Hard Suite Cross-Check

The hard suite was tightened so visible prompts match hidden success signals: the exact-test repair names `math.go` and `math_test.go`, bans bash discovery, and the missing-file recovery prompt explicitly requires `manifests/production.yaml` in the final answer.

| Prompt variant | Tool calls | Invalid calls | Validation issues | Completed |
| --- | ---: | ---: | ---: | ---: |
| deepseek-harness-compact-v10 | 18 | 0 | 0 | 8/8 |

## Limits Suite Results

| Prompt variant | Tool calls | Invalid calls | Validation issues | Completed |
| --- | ---: | ---: | ---: | ---: |
| deepseek-harness-max-v1 | 20 | 4 | 9 | 10/10 |
| deepseek-harness-compact-v7 | 16 | 0 | 3 | 10/10 |
| deepseek-harness-compact-v8 | 16 | 0 | 1 | 10/10 |
| deepseek-harness-compact-v9 | 20 | 4 | 8 | 10/10 |
| deepseek-harness-compact-v10 | 16 | 0 | 0 | 10/10 |

## Gauntlet Cross-Check

| Prompt variant | Tool calls | Invalid calls | Validation issues | Completed |
| --- | ---: | ---: | ---: | ---: |
| deepseek-harness-max-v1 | 16 | 0 | 1 | 10/10 |
| deepseek-harness-compact-v10 | 16 | 0 | 0 | 10/10 |

## Adversarial Cross-Check

| Prompt variant | Tool calls | Invalid calls | Validation issues | Completed |
| --- | ---: | ---: | ---: | ---: |
| deepseek-harness-max-v1 | 11 | 0 | 0 | 5/5 |

## Learnings

- DeepSeek responds strongly to compact, explicit tool-choice rules: known files should go straight to `read`; no-bash tasks must not use `bash`; file paths are runtime strings, not markdown.
- Checklist questions outperformed "hard laws." `deepseek-harness-compact-v9` looked stronger on paper, but repeated forbidden `bash` calls in the decoy review. v8/v10's "Is this tool allowed?" preflight worked better.
- Exact final-answer scoring must be visible in the user prompt. Two hidden-signal mismatches were corrected before treating the run as a durable profile.
- The previous `2>&1` habit disappeared in the corrected hard and limits v10 runs once the prompt combined exact-command copying with the bash contract that stderr is already captured.
- The remaining harness-learning posture is profile-driven: keep stress profiles separate until profile consumption rules decide whether to tune prompts, schema descriptions, retry messages, or tool semantics.
- The external `yasasbanukaofficial/claude-code` repository was treated only as high-level design inspiration because its README says the mirrored source is proprietary leaked code. No prompt text or source was copied from it.

## Saved Artifacts

- Hard suite profile: `profiles/deepseek/deepseek-v4-pro.hard-harness-max-v1.{json,md}`
- Gauntlet profile: `profiles/deepseek/deepseek-v4-pro.gauntlet-harness-max-v1.{json,md}`
- Adversarial profile: `profiles/deepseek/deepseek-v4-pro.adversarial-harness-max-v1.{json,md}`
- Limits compact profile: `profiles/deepseek/deepseek-v4-pro.limits-compact-v10.{json,md}`
- Hard compact profile: `profiles/deepseek/deepseek-v4-pro.hard-compact-v10.{json,md}`
- Gauntlet compact profile: `profiles/deepseek/deepseek-v4-pro.gauntlet-compact-v10.{json,md}`
- Hard suite run: `.runs/deepseek-v4-pro-hard-harness-max-v1/`
- Gauntlet run: `.runs/deepseek-v4-pro-gauntlet-harness-max-v1/`
- Adversarial run: `.runs/deepseek-v4-pro-adversarial-harness-max-v1b/`
- Limits compact run: `.runs/deepseek-v4-pro-limits-compact-v10/`
- Corrected hard compact run: `.runs/deepseek-v4-pro-hard-compact-v10b/`
- Gauntlet compact run: `.runs/deepseek-v4-pro-gauntlet-compact-v10/`
