# Plan: Installable plugin bundles and marketplace — Epic #748

## Context

- Problem: end users cannot install reusable skills, commands, agents, hooks, or MCP configuration without rebuilding the Go binary.
- User impact: users can install versioned bundles from local paths or Git sources, discover marketplace bundles, and safely control visibility separately from executable trust.
- Constraints: preserve compile-time Go plugins; reuse the existing skill, MCP, profile, and hooks loaders; never execute repository content at install time; one test-gated commit per child issue.

## Scope

- In scope: bundle manifest/layout validation, fetch/install/update lifecycle, persistent installed state, CLI and TUI management, plugin registration, marketplace discovery, docs.
- Out of scope: signing, bundle dependencies, automatic update daemons, remote publishing, and changes to compile-time Go plugins.

## Documentation Contract

- Feature status: implemented
- Public docs affected: design documentation and CLI help.
- Spec docs to update before code: this plan and the bundle design document.
- Implementation notes to add after code: engineering/system logs and CLAUDE current-source-of-truth guidance.

## Test Plan (TDD)

- New failing tests to add first: a focused acceptance/negative-path test for every slice below.
- Existing tests to update: daemon bootstrap, CLI dispatcher, skills reload, hooks, MCP/profile wiring, and TUI overlays.
- Regression tests required: every slice's package suite; final `gofmt -l .`, `go vet ./...`, and `./scripts/test-regression.sh`.

## Implementation Checklist

- [x] #775 Define plugin bundle manifest and layout validation.
- [x] #776 Fetch/install local paths, git URLs, and GitHub shorthand safely.
- [x] #777 Persist installed state with separate enable and trust flags.
- [x] #778 Add `harnesscli plugin install|list|uninstall|update`.
- [x] #779 Register enabled bundle skills and commands via `internal/skills`.
- [x] #780 Register enabled+trusted agents and MCP configuration via profiles/MCP validation.
- [x] #781 Load enabled+trusted hooks through `internal/hooks`.
- [x] #782 Add marketplace source configuration and CLI management.
- [x] #783 Add TUI plugin browser modal and keyboard coverage.
- [x] Update design docs, indexes, durable logs, and CLAUDE.
- [ ] Run full regression and open (without merging) the requested PR.

## Risks and Mitigations

- Risk: remote bundles can introduce executable configuration. Mitigation: validate manifests before copying, prevent traversal/symlinks, and gate hooks/MCP/agents on an independent trust flag.
- Risk: parallel loaders drift. Mitigation: plugin integration only produces inputs consumed by the existing skill, profile, MCP, and hook APIs.
- Risk: long-lived branch conflicts. Mitigation: keep nine self-contained commits and avoid unrelated changes.
