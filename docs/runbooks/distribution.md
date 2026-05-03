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

Homebrew is the first polished macOS distribution target.

Formula responsibilities:

- install `go-code`, `harnesscli`, and `harnessd`
- install `prompts/` and `catalog/` under `share/go-code`
- patch or wrap `go-code` so it can find `share/go-code`
- run a lightweight smoke command such as `go-code --help`

Expected install UX:

```bash
brew tap dennisonbertram/go-code
brew install go-code
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
