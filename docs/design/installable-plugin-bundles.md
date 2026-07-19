# Installable Plugin Bundles

Status: implemented (Epic #748).

An installable bundle has `plugin.json` with schema version, kebab-case name, version, and optional relative `skills`, `commands`, `agents`, `hooks`, and `mcp` paths. Bundles install to `~/.go-harness/plugins/<name>/<version>` from a local directory, Git URL, or GitHub shorthand. Install validates the manifest and rejects traversal/symlinks before promotion.

Enable and trust are separate persisted state. Enabled bundles contribute skills and commands through the existing skills/TUI command loaders. Only enabled and trusted bundles contribute agent TOML profiles, validated MCP server configs, and lifecycle hooks. Remote bundles default untrusted.

Marketplace sources are local JSON indexes in this iteration, managed by `harnesscli plugin marketplace add|list|update`; a marketplace advertises name, description, and install source.
