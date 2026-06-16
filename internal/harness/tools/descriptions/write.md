Write content to a workspace file. Creates the file and any parent directories if they do not exist. Use this tool to create NEW files or to completely replace the contents of an existing file.

## WHEN TO USE: write vs other tools
- **write**: creating a brand-new file, or fully replacing the contents of an existing file from scratch.
- **edit**: changing a single line, inserting a section, or modifying part of an existing file without rewriting everything.
- **apply_patch**: applying a pre-computed diff, or making multi-file bulk changes atomically.

## WHEN NOT TO USE
- Do NOT use write when you only need to change a single line or function in an existing file — use edit instead. Write replaces the ENTIRE file, which means you must reproduce all unchanged content from memory.
- Do NOT use write for bulk, multi-file changes — use apply_patch with a unified diff.
- Do NOT use write (without append=true) just to add a few lines at the end of an existing file — use edit or set append=true.

## Common mistakes
- WRONG: write(path="server.go", content="...") to fix one typo — the agent must reproduce the entire file from memory. Prefer edit for targeted changes.
- WRONG: write(path="config.json", content="...") with invalid JSON — the tool rejects .json files that do not parse as valid JSON. Fix the JSON and retry.
- WRONG: write(path="existing.go", content="...") without reading the file first — if the file already has content you cannot see, you will silently overwrite it. Read first, then write.

## Behavioral rules
- For .JSON files: content is validated as valid JSON before writing. If invalid, the tool returns an "invalid_json" error and does NOT write the file. Fix the JSON structure and retry.
- Path must be relative to the workspace root. Absolute paths are rejected.
- If a file already exists and append is false (the default), the entire file is replaced. You are responsible for including all needed content — nothing is preserved from the previous version.
- The "expected_version" parameter enables optimistic concurrency: pass the version string from a previous read or write result, and the write will fail with a stale_write error if the file has been modified since then.
- Content can be provided via the "content" parameter (or its aliases: "new_text", "new_string", "text").
