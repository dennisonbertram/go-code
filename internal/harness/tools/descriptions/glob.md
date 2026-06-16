Find files by filename pattern using glob wildcard syntax. Use this to locate files when you know part of the filename, extension, or directory name but not the exact path.

## WHEN TO USE: glob vs other tools
- **glob**: finding files by NAME (filename, extension, directory name).
- **grep**: searching file CONTENTS for a string or pattern. Use grep if you need to find text inside files, not just file names.
- **ls**: listing the contents of a specific directory. Use ls when you know which directory to look in.
- **find**: combining name patterns with other filters (modification time, file size, etc.). Use bash `find` for complex predicate searches.

## WHEN NOT TO USE
- Do NOT use glob to search file contents — glob matches file and directory NAMES only. Use grep for content search.
- Do NOT use glob with "**" recursive patterns — Go's filepath.Glob does NOT support "**". Use multiple levels of "*" instead, or use ls with recursive=true.

## Common mistakes
- WRONG: glob(pattern="**/*.go") — this does NOT work. Go's filepath.Glob does not support "**". Use */*.go, */*/*.go, etc. instead.
- WRONG: using glob to find files containing a specific string. glob searches NAMES, not contents. Use grep.

## Behavioral rules
- Each "*" matches within a single directory level only. To find files in deeply nested directories, use multiple levels of "*".
- To find ALL files of a type across the entire project, call glob multiple times with increasing depth: */*.ext, */*/*.ext, */*/*/*.ext, etc. Or use ls with recursive=true and then filter by extension.

Common patterns:
- */*.go — Go files one level deep (e.g. cmd/main.go)
- */*/*.go — Go files two levels deep (e.g. internal/harness/runner.go)
- */*/*/*.go — Go files three levels deep
- docs/*.md — Markdown files directly in docs/
- docs/*/*.md — Markdown files one level under docs/
- */*_test.go — test files one level deep
- */*/*_test.go — test files two levels deep
- prompts/*.yaml — YAML files in prompts/

The pattern is relative to the workspace root. Returns a list of matching file paths. Use max_matches to limit results (default 500, max 2000).
