Show changes in the workspace git repository as a unified diff, including line-level additions, deletions, and context. Use this tool instead of running `git diff` via bash.

Parameters:
- path (optional): Relative file path to scope the diff to a single file.
- staged (optional, boolean): When true, shows only staged (cached) changes (equivalent to `git diff --staged`). Default false shows unstaged working-tree changes.
- target (optional): A git revision or range to diff against. Examples: `HEAD~3`, `main`, `abc123..def456`, `main...feature`. Passed directly to `git diff <target>`. Targets beginning with `-` are rejected.
- max_bytes (optional, integer 1-1048576): Truncate output at this byte limit. Default 262144 (256 KB).

Returns a JSON object with fields: diff (string), truncated (bool), exit_code (int), timed_out (bool).

Note: This tool produces a standard unified diff. It does not support flags like --stat, --name-only, or --color. For those, use the bash tool.