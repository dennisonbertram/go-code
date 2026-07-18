Show the diff between two git refs (commits, branches, or tags). Use this tool to compare the state of the repository or a specific file between any two points in history.

Parameters:
- from (required): Base ref (commit hash, branch name, or tag). Refs beginning with `-` are rejected.
- to (optional): Target ref (commit hash, branch, or tag). Default is HEAD. Refs beginning with `-` are rejected.
- path (optional): Limit the diff to a specific file or directory (relative to workspace root).
- stat_only (optional, boolean, default false): When true, returns only the file change summary (--stat), not the full diff. Useful for a quick overview before requesting the full diff.
- max_bytes (optional, integer, default 262144): Truncate diff output at this byte limit.

Returns a JSON object with:
- from: the base ref
- to: the target ref
- diff: the unified diff (empty when stat_only=true)
- stat: the file change summary (always present)
- files_changed: number of files with changes
- insertions: total lines added
- deletions: total lines removed
- truncated: whether the diff was cut off at max_bytes

Note: Use stat_only=true first to gauge scope, then request the full diff for specific files.
