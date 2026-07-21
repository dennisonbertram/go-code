# Distribution Runbook

This runbook describes the current install path and the intended distribution ladder for `go-code`.

## Current Channel: Source Install

The supported local distribution channel is the source installer:

```bash
git clone https://github.com/dennisonbertram/go-code.git
cd go-code
./scripts/install.sh --add-to-path
```

The default install is user-local and does not need sudo:

- binaries: `~/.local/bin/go-code`, `~/.local/bin/harnesscli`, `~/.local/bin/harnessd`
- runtime assets: `~/.local/share/go-code/prompts` and `~/.local/share/go-code/catalog`

The installer also supports:

```bash
make install
./scripts/install.sh --prefix "$HOME/.local"
./scripts/install.sh --bin-dir "$HOME/bin" --data-dir "$HOME/.local/share/go-code"
./scripts/install.sh --uninstall
sudo ./scripts/install.sh --system
```

Use `--system` only when a system-wide install is explicitly desired. The default must remain sudo-free.

## Installed Command Contract

`go-code` is the user-facing command:

```bash
go-code              # launch the TUI from the current project
go-code "prompt"     # run one prompt from the current project
go-code --server     # start harnessd and leave it running
```

The wrapper must:

- auto-start `harnessd` when no server is healthy at the configured port
- leave an already-running server alone
- locate installed `prompts/` and `catalog/` assets outside the repo checkout
- resolve the caller's project root and pass it as the run workspace
- work when launched from any repository, not just from this repo

## OS Service Install

End users can run `harnessd` as a persistent, user-level OS service instead of a hand-managed process. No sudo is needed:

```bash
harnesscli service install    # write the unit file
harnesscli service start      # start it now
harnesscli service status     # installed? running? healthy?
harnesscli service stop       # stop it
harnesscli service uninstall  # stop (best-effort) and remove the unit file
```

What gets written:

| Platform | Unit file | Manager |
| --- | --- | --- |
| macOS | `~/Library/LaunchAgents/com.gocode.harnessd.plist` (label `com.gocode.harnessd`) | launchd user agent in `gui/<uid>` |
| Linux | `~/.config/systemd/user/harnessd.service` | `systemd --user` |

- The macOS agent sets `RunAtLoad` and `KeepAlive`, so the daemon starts at login and relaunches on crash. The Linux unit sets `Restart=on-failure` and installs into `default.target`, so it starts with the user session and restarts on failure.
- Logs go to `~/.harness/logs/harnessd.stdout.log` and `~/.harness/logs/harnessd.stderr.log` on both platforms (override the directory with `--log-dir`). On Linux, daemon output goes to those files; `journalctl --user -u harnessd` shows unit lifecycle events.

`install` flags:

- `--binary PATH`: the `harnessd` executable to run. Default: `harnessd` looked up on `PATH` (the Homebrew- or installer-provided binary). The unit embeds the absolute path.
- `--addr ADDR`: listen address exported to the daemon as `HARNESS_ADDR`. Default: the same resolution the daemon applies to itself — `HARNESS_ADDR` env or `~/.harness/config.toml`, falling back to `:8080`.
- `--log-dir DIR`: log directory (default `~/.harness/logs`).
- `--dry-run`: print the rendered unit and its target path without writing anything.

Lifecycle notes:

- `install` only writes the unit file; it does not start the daemon. Run `harnesscli service start`, or log out and back in (login triggers `RunAtLoad` / `default.target`).
- `start` is idempotent on macOS: an already-loaded agent is restarted with `launchctl kickstart -k`.
- `status` exits non-zero with a clear message when the service is not installed. Otherwise it reports the running state and probes `GET <base-url>/healthz` (`--base-url` flag, default `http://localhost:8080`), so "running, healthy" is distinguished from "running but unreachable".
- `uninstall` best-effort stops and disables the service (`launchctl bootout` / `systemctl --user disable --now`), then removes the unit file. It fails with a clear "not installed" message when the service was never installed.
- `start`, `stop`, and `status` also fail with the same "not installed" message when run before `install`.

Troubleshooting:

- macOS state query: `launchctl print gui/$(id -u)/com.gocode.harnessd`.
- Linux state query: `systemctl --user status harnessd`; lifecycle events: `journalctl --user -u harnessd`.
- Linux boot persistence without an interactive login requires lingering: `loginctl enable-linger "$USER"`.
- If the `harnessd` binary moves, re-run `harnesscli service install --binary <new-path>` — the unit embeds the absolute path and is not watched for changes.
- The service managers do not create the log directory; `install` creates it. If you deleted it, re-run `install` or `mkdir -p ~/.harness/logs`.

Scope guardrails: user-level services only — no root launchd daemons, no system systemd units, no Windows support. Repository dev agents keep using tmux per the worktree runbooks; the service install targets end users running go-code as a tool.

## GitHub Pages

The public landing page source lives in `docs/site/`.

The Pages workflow lives in `.github/workflows/pages.yml` and publishes `docs/site` with GitHub Actions.

After deployment, the expected project page is:

```text
https://dennisonbertram.github.io/go-code/
```

Repository setup:

1. Open repository Settings.
2. Go to Pages.
3. Set Build and deployment source to GitHub Actions.
4. Push to `main` or run the workflow manually.

The page is intentionally static. Do not add a framework unless the site grows beyond a single-page install and product overview.

## Release Archive Layout

The next distribution channel should be GitHub Releases with per-platform archives:

```text
go-code_Darwin_arm64.tar.gz
go-code_Darwin_x86_64.tar.gz
go-code_Linux_x86_64.tar.gz
go-code_Linux_arm64.tar.gz
```

Each archive should contain:

```text
bin/go-code
bin/harnesscli
bin/harnessd
share/go-code/prompts/
share/go-code/catalog/
```

Release builds should run:

```bash
go test ./cmd/harnesscli/... ./cmd/harnessd ./internal/... -count=1
go build -o dist/bin/harnesscli ./cmd/harnesscli
go build -o dist/bin/harnessd ./cmd/harnessd
```

Then copy `scripts/go-code.sh` to `dist/bin/go-code` and copy `prompts/` plus `catalog/` into `dist/share/go-code/`.

## Installer Download Mode

After release archives exist, extend `scripts/install.sh` with a download mode:

```bash
curl -fsSL https://dennisonbertram.github.io/go-code/install.sh | bash
```

Download-mode requirements:

- detect OS and architecture
- download the matching release archive
- verify checksum before install
- install into the same `bin/` and `share/go-code/` layout used today
- preserve `--prefix`, `--bin-dir`, `--data-dir`, `--uninstall`, and `--system`
- keep source-build mode for contributors

## Homebrew Tap

Homebrew is the first polished macOS distribution target. The repo includes
`Formula/go-code.rb`, so users can install the latest `main` build with one
command:

```bash
brew install --HEAD dennisonbertram/go-code/go-code
```

Homebrew resolves that command through the separate tap repository
`dennisonbertram/homebrew-go-code`. When `Formula/go-code.rb` changes here,
mirror the same formula into that tap before advertising the command.

Formula responsibilities:

- install `go-code`, `harnesscli`, and `harnessd`
- install `prompts/` and `catalog/` under `share/go-code`
- keep `go-code` able to find `share/go-code`
- run a lightweight smoke command such as `go-code --help`

The explicit tap flow also works:

```bash
brew tap dennisonbertram/go-code
brew install --HEAD go-code
go-code
```

## Long-Term Simplification

The cleanest eventual packaging shape is one binary:

```bash
go-code
go-code --server
go-code "prompt"
```

That binary can embed prompt and catalog assets with Go `embed`, removing the need to distribute three commands plus a shared asset directory.

Do not jump to this until the current multi-binary installer has enough daily-use mileage.

## Release Checklist

Before publishing a release:

1. Run the focused installer checks:

   ```bash
   bash -n scripts/install.sh scripts/go-code.sh
   scripts/install.sh --dry-run --no-build --prefix "$PWD/.tmp/install-dry-run"
   GOCACHE=/tmp/go-build scripts/install.sh --prefix "$PWD/.tmp/install-verify"
   .tmp/install-verify/bin/go-code --help
   ```

2. Run the CLI/TUI regression scope:

   ```bash
   HOME=$(mktemp -d) GOCACHE=/tmp/go-build go test ./cmd/harnesscli/... -count=1
   ```

3. Build both product binaries:

   ```bash
   GOCACHE=/tmp/go-build go build -o ./harnesscli ./cmd/harnesscli
   GOCACHE=/tmp/go-build go build -o ./harnessd ./cmd/harnessd
   ```

4. Smoke from outside the repo:

   ```bash
   cd /tmp
   go-code --help
   ```

5. Confirm `go-code` can launch a run from another project and the server receives that project as `workspace_path`.
