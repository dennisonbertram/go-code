# Playground

`playground/` holds experimental, training, and snippet-style Go code that is useful for practice and exploration but is not part of the main harness product surface.

Why it is isolated:

- The main application lives under `cmd/`, `internal/`, `plugins/`, and supporting repo directories.
- The playground code has intentionally looser quality and packaging constraints than product code.
- Keeping it in a separate Go module prevents example breakage from polluting product-level verification and keeps the repo root focused on the real application layout.

Working with it:

```bash
cd playground
go test ./...
```

Training exercises and one-off benchmark solutions live in `playground/training/` so the repository root stays focused on the product. Some nested training exercises include their own `go.mod`; run those from their own directory when needed.

`examples/` and `exercises/` are intentionally isolated as their own modules because they contain incomplete practice code. Test those packages individually when working on them; they are not part of the main product or stable playground baseline.

Treat the playground as a sandbox. Product-quality changes should land in the main module instead.
