# Installable Plugin Bundles

Status: implemented (Epic #748). Manifest v1 authoring contract and plugin-home decision published under Epic #821 (slice 1); trust grant/revoke CLI and install-time confirmation (slice 2) and zip/GitHub-archive install sources (slice 3) implemented. Items marked **planned** are later slices of Epic #821 and are not implemented yet.

An installable bundle is a directory with a `plugin.json` manifest declaring optional `skills`, `commands`, `agents`, `hooks`, and `mcp` components. Bundles are installed from a local directory, git URL, or GitHub shorthand, validated without executing their content, and atomically promoted into a versioned plugin home. This document is the stable v1 authoring contract for that manifest and the lifecycle around it.

This system is distinct from the compile-time Go plugins under the repository's top-level `plugins/` directory (see `docs/design/plugins.md`), which are unrelated to installable bundles.

## 1. Plugin home decision

There is exactly one installable bundle home:

- **Bundle home (canonical):** `$HARNESS_GLOBAL_DIR/plugins`, defaulting to `~/.go-harness/plugins` when `HARNESS_GLOBAL_DIR` is unset. `harnesscli plugin ...`, `harnessd` startup wiring, and the TUI bundle command loader all resolve this root.

- **Legacy home (deprecated, still supported):** `~/.config/harnesscli/plugins/*.json` — single-file JSON `PluginDef` slash commands loaded by the TUI at startup. These keep loading indefinitely, but the format is frozen: no new capabilities will be added to it. When the directory exists and contains at least one `*.json` file, the TUI surfaces a startup warning pointing at the bundle format.

Rationale: every managed surface (installer, state store, marketplace store, daemon skill/hook/MCP wiring, TUI trusted-command loader) already anchors on `$HARNESS_GLOBAL_DIR/plugins`; the legacy directory predates bundles and can only express prompt/bash slash commands, not skills, agents, hooks, or MCP servers.

### Migrating a legacy JSON plugin to a bundle

1. Create a bundle directory with a `plugin.json` (see §2) and a `commands/` directory.
2. Move each legacy `*.json` file into `commands/` unchanged — bundle command directories accept the same `PluginDef` schema.
3. `harnesscli plugin install /path/to/the/bundle` (a local install, so it is trusted by default).
4. Restart the TUI, verify the command works, then delete the legacy file from `~/.config/harnesscli/plugins/`.

## 2. Manifest v1 reference (`plugin.json`)

The manifest is a single JSON object at the bundle root. Unknown fields are rejected (`json.Decoder.DisallowUnknownFields`), so typos fail install instead of being silently ignored.

| Field | Type | Required | Rules |
| --- | --- | --- | --- |
| `schema_version` | integer | yes | Must be `1`. |
| `name` | string | yes | Kebab-case: `^[a-z0-9]+(?:-[a-z0-9]+)*$`. |
| `version` | string | yes | Safe path segment: `^[A-Za-z0-9][A-Za-z0-9._+-]*$`, never `.`, `..`, or absolute. Becomes the versioned install directory name. |
| `description` | string | no | Free text shown in listings. |
| `skills` | string | no | Relative path to a **directory** of skills (`SKILL.md` trees), loaded by the standard skills loader. |
| `commands` | string | no | Relative path to a **directory** of slash-command definitions. Today each `*.json` file uses the legacy `PluginDef` schema (`name`, `description`, `handler` = `bash`/`prompt`, `command` or `prompt_template`). Markdown command files with `$ARGUMENTS` are **planned** (Epic #821, slice 4). |
| `agents` | string | no | Relative path to a **directory** of agent TOML profiles, searched when resolving `--profile`. |
| `hooks` | string | no | Relative path to a hook **file** (`*.json`, config-driven hooks schema: `event`, `kind` = `command`/`http`, `command`/`url`, optional `matcher`, `timeout_seconds`; see `docs/design/plugins.md`). Every `*.json` file in the declared file's directory is loaded. |
| `mcp` | string | no | Relative path to an MCP servers **file**: a JSON array of `{name, transport, command, args, url}` entries (`transport` = `stdio` or `http`; `command`/`args` for stdio, `url` for http), validated by the same parser as `HARNESS_MCP_SERVERS`. |

Path rules, enforced at install and on every load:

- Declared paths must be relative and contained in the bundle root — absolute paths and `..` escapes are rejected.
- `skills`, `commands`, `agents` must be existing directories; `hooks`, `mcp` must be existing files.
- Install rejects any symlink anywhere in the staged bundle.
- The loader only ever reads metadata and declared content; validation never executes bundle code.

Validation errors name the offending field, e.g. `plugin manifest schema_version must be 1`, `plugin manifest name "My_Plugin" must be kebab-case`, `plugin manifest version ".." must be a safe path segment`, `skills: declared path "skils": ...`.

### Example

```json
{
  "schema_version": 1,
  "name": "release-helper",
  "version": "1.0.0",
  "description": "Release-notes summarizer and release-flow hooks",
  "skills": "skills",
  "commands": "commands",
  "hooks": "hooks/hooks.json",
  "mcp": "mcp.json"
}
```

```
release-helper/
├── plugin.json
├── skills/
│   └── release-notes/SKILL.md
├── commands/
│   └── summarize.json        # PluginDef: {"name":"summarize","handler":"prompt","prompt_template":"Summarize: {args}", ...}
├── hooks/
│   └── hooks.json            # config-driven hook definition(s)
└── mcp.json                  # [{"name":"gh","transport":"stdio","command":"gh-mcp","args":["serve"]}]
```

## 3. Install layout and lifecycle

```
~/.go-harness/plugins/            # $HARNESS_GLOBAL_DIR/plugins
├── state.json                    # per-plugin lifecycle state (enabled/trusted/source)
├── marketplaces.json             # marketplace source indexes
└── release-helper/               # <name>
    └── 1.0.0/                    # <version> — the promoted bundle root
        └── plugin.json ...
```

- **Sources** (`harnesscli plugin install <source>`): local directory path, local zip file, git URL, zip URL (`.zip` suffix), GitHub archive URL (`github.com/.../archive/...`, with or without suffix), or `owner/repo` shorthand (expands to `https://github.com/owner/repo.git`). Remote git sources are cloned with `git clone --depth 1`; remote zips are fetched over HTTP; local directories are copied and local zips read from disk. Zip archives are extracted with stdlib `archive/zip`: entry names are validated before anything is written (no absolute paths, no `..` elements, no backslashes, no symlink entries), and a single shared top-level directory — the GitHub archive convention — is stripped so the bundle root lands at the staging dir. Local zips are non-remote (trusted by default); zip URLs are remote (untrusted by default).
- **Staging and promotion:** the source lands in a private `<root>/.install-*` staging directory, symlinks are rejected, the manifest is validated, and the tree is atomically renamed to `<name>/<version>` (`Installer.Stage` → `StagedBundle.Promote`; `StagedBundle.Discard` abandons a stage). A failed or declined install leaves no partial bundle and no state record behind.
- **State:** `state.json` records `name`, `version`, `source`, `remote`, `enabled`, `trusted` per plugin (`plugins.StateStore`). Re-installing an existing name (e.g. `harnesscli plugin update`) replaces the version directory but preserves the user's `enabled`/`trusted` flags — an update can never silently broaden execution authority.
- **Remote confirmation:** installing a remote bundle prints its declared executable surfaces (skills/commands/agents/hooks/mcp) and requires confirmation before promotion — an interactive y/N prompt on a terminal, `--yes`/`-y` for scripts, refusal otherwise. Local installs proceed without prompting. `plugin update` re-prints the surfaces and re-requires confirmation for a remote bundle only when they changed; an unchanged remote update needs no confirmation and preserves trust.
- **CLI:** `harnesscli plugin install|list|uninstall|update|trust|untrust|marketplace`. `plugin list` prints `enabled=` and `trusted=` per bundle, with an `untrusted — commands/hooks/MCP inactive` hint on untrusted entries. `plugin trust <name>` / `plugin untrust <name>` grant and revoke executable authority.
- **Activation:** bundle contents are discovered at process start. Skills, hooks, and MCP servers take effect at the next `harnessd` start; slash commands at the next TUI start. There is no hot-reload into a running daemon.

## 4. Trust model

`enabled` and `trusted` are independent persisted flags:

- **Enabled** controls visibility: whether the bundle contributes anything at all.
- **Trusted** controls executable authority: whether the bundle may run code or configuration with side effects.

Defaults at install: **local installs are trusted; remote installs are untrusted** — remote content crosses a trust boundary and stays inert until the user explicitly grants trust with `harnesscli plugin trust <name>` (revoke with `plugin untrust <name>`). Trust changes apply at the next `harnessd`/TUI start; there is no daemon hot-reload.

| Declared surface | Requires |
| --- | --- |
| `skills` (SKILL.md trees into the skills loader) | enabled |
| `commands` (TUI slash commands; `bash` handler can run a shell) | enabled **and** trusted |
| `agents` (TOML profiles for `--profile`) | enabled **and** trusted |
| `hooks` (shell/HTTP lifecycle hooks) | enabled **and** trusted |
| `mcp` (MCP server configs registered at startup) | enabled **and** trusted |

Untrusted bundles are therefore visible in `plugin list` but contribute nothing executable — fail-closed by construction: untrusted bundles never reach the hooks loader, the MCP registrar, the profile search path, or the TUI command registry.

Trust follows the semantics of the config-driven hooks trust model (`internal/hooks/trust.go`): trust is an explicit, user-owned grant recorded locally — not a sandbox. A trusted bundle's hooks and commands execute with the user's privileges, so only trust bundles whose content you have reviewed. Sandboxing plugin code is an explicit non-goal.

## 5. Marketplace

Marketplace sources are local JSON indexes managed by `harnesscli plugin marketplace add|list|update`; a marketplace advertises name, description, and install source for each bundle. Installing from a marketplace entry uses the same installer and trust defaults as any other source.
