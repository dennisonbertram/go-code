# Runbooks Index

- [Session rewind](session-rewind.md) — destructive file-snapshot restore and conversation truncation.

- `testing.md`: How to design and run meaningful tests before commit.
- `symphony.md`: How to run OpenAI Symphony for this repository with the wrapper script.
- `symphony-issue-authoring.md`: How to write GitHub issues that Symphony can execute autonomously with strict TDD, behavior tests, regression gates, and merge rules.
- `deployment.md`: MVP deployment runbook with security and verification checks.
- `distribution.md`: Installer, GitHub Pages, release archive, Homebrew, and release checklist guidance for distributing go-code.
- `worktree-flow.md`: Required worktree-first development and merge workflow.
- `issue-triage.md`: How to capture bugs/problems as GitHub issues.
- `documentation-maintenance.md`: How to maintain per-folder indexes and documentation quality.
- `ownership-copy-semantics.md`: Checklist for reviewing slices, maps, pointers, and nil semantics at storage/export boundaries.
- `provider-model-impact-mapping.md`: When provider/model feature work must ship with a cross-surface impact map and how to write it.
- `harnesscli-live-testing.md`: End-to-end tmux runbook for `harnessd` + `harnesscli`, including variables, commands, and known issues.
- `tui-visual-testing.md`: Automated frame-audit framework and manual walk guidance for TUI visual quality and work-readiness.
- `observational-memory.md`: Configuration, operation, events, and recovery guidance for observational memory.
- `terminal-bench-periodic-suite.md`: How to run and interpret the private Terminal Bench smoke suite for the harness.
- `mcp.md`: How to add MCP servers to the harness (client), and how to expose the harness as an MCP server.
- `profile-authoring.md`: TOML schema reference, resolution tiers, built-in profile catalog, and step-by-step guide for creating and validating custom profiles.
- `profile-operations.md`: How to choose, start with, and operate profiles — recommendation tool, run request field, API lifecycle management, and efficiency reports.
- `subagent-debugging.md`: How to find a child run's ID, read its status and events, interpret ChildResult, and diagnose common failures (profile not found, tool not allowed, max_steps exceeded, cost limit, workspace provision).
- `benchmark-smoke.md`: Key-free deterministic smokes (in-process Go test + shell script), the grounded result schema (`internal/benchresult`), the comparison-harness shape (`benchmarks/comparison/`), and the real Python benchmark paths with their deps and honesty caveats.
