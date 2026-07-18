Show per-line blame for a file with full commit context (author, date, and commit message). Use this tool to answer "who wrote this line?", "why was this changed?", or "when was this introduced?".

Parameters:
- path (required): File path relative to workspace root.
- start_line (optional, integer >= 1): First line to blame (1-indexed). When omitted, blames the entire file.
- end_line (optional, integer >= 1): Last line to blame (inclusive). Required when start_line is provided.
- rev (optional): Git revision to blame at (commit hash, branch, tag). Default is HEAD. Revisions beginning with `-` are rejected.

Returns a JSON object with:
- file: the file path
- rev: the revision blamed at
- lines: array of {line_number, content, commit_hash, short_hash, author_name, author_email, date, commit_subject, commit_body}
- unique_commits: number of distinct commits in the result

Note: For large files, always specify start_line and end_line to avoid token-heavy output.
