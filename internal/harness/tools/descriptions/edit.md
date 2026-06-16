Edit (modify) a workspace file by replacing text. Use this tool to make targeted changes to existing files — replace specific strings or code blocks with new content. Requires the file to already exist; for creating new files, use the write tool instead.

## WHEN TO USE: edit vs other tools
- **edit**: changing a function name, fixing a typo, replacing a line, inserting a section — any single-file, targeted text replacement.
- **write**: creating a brand-new file, or completely replacing the entire contents of an existing file.
- **apply_patch**: renaming a symbol across many files, applying a pre-computed diff, or making multiple related edits in one file.

## WHEN NOT TO USE
- Do NOT use edit to create a new file — the file must already exist. Use write for new files.
- Do NOT use edit to apply changes across multiple files at once — use apply_patch for bulk, multi-file edits.
- Do NOT use edit for sed/awk-style pattern substitutions — write uses exact-string matching, not regex.

## Common mistakes
- WRONG: edit(path="new_file.go", old_text="...", new_text="...") when the file does not yet exist. Edit requires the file to already exist.
- WRONG: using edit in a loop to rename a symbol across dozens of files. Prefer apply_patch with a unified diff.
- WRONG: forgetting to include surrounding whitespace/newlines in old_text so the match silently fails. old_text must match exactly.

## Behavioral rules
- You MUST Read the file in the same conversation before calling edit, or the edit will be REJECTED. This is a strict requirement, not a recommendation.
- old_text must be unique within the file (unless replace_all is true). If the match is ambiguous, the edit is rejected.
- old_string and new_string MUST be different — the edit is rejected if they are identical.
- Check the edit result — the tool reports whether the replacement was applied. If the edit failed, re-read the file to get its current state before retrying.

Parameters:
- path (required): Relative file path inside the workspace (e.g. "go.mod", "internal/server/http.go").
- old_text (required): The exact text to find and replace. Must match the file exactly, including indentation and whitespace.
- new_text (required): The replacement text. Must be different from old_text.
- replace_all (optional): If true, replace every occurrence of old_text. Default false (replace only the first match).
- expected_version (optional): Version hash for optimistic concurrency. If the file has changed since you last read it, the edit is rejected with a stale_write error.
- start_line_hash (optional): 12-character hex hash of the first line of old_text. If provided, the edit is rejected if no line matches that hash or if the hashed line does not match the first line of old_text. Use hash_lines=true on the read tool to obtain these hashes.
- end_line_hash (optional): 12-character hex hash of the last line of old_text. If provided, the edit is rejected if no line matches. Useful to confirm the edit range is still intact before applying.

Hash-based addressing is additive — the existing old_text/new_text replacement logic is unchanged. Hashes serve as pre-flight validation anchors to prevent silent mismatches when surrounding code has shifted.

Supports multi-line old_text and new_text for replacing code blocks, function bodies, or multi-line strings. The old_text must match exactly (including whitespace and newlines).
