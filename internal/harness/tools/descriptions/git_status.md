Show the git status of the workspace repository. Reports staged, unstaged, modified, untracked, and deleted files. Use this tool to check whether the working tree is clean or dirty, which files have been added to the index, and which are new or pending commit.

## WHEN TO USE: git_status vs other tools
- **git_status**: checking overall working-tree state, seeing which files are modified/added/deleted, checking branch name and ahead/behind counts.
- **git_diff**: viewing actual line-level diffs inside files. Use git_diff to see what changed, not just which files changed.
- **git_file_history**: tracking the history of specific files over time.

## WHEN NOT TO USE
- Do NOT use git_status to see what actually changed inside a file — it only reports file-level status codes (M, A, D, ??). Use git_diff for line-level diffs.
- Do NOT use git_status to check commit history or file history — use git_file_history or git_log_search instead.

## Common mistakes
- WRONG: calling git_status, then using the output to understand code changes. Status only tells you WHICH files changed, not WHAT changed inside them.

Returns a compact summary equivalent to `git status --short`, including the current branch name, ahead/behind counts relative to upstream, and a per-file status code (M = modified, A = added, D = deleted, ?? = untracked).
