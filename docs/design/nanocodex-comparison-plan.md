# Cleaner Boundaries and Tool Ergonomics (nanocodex comparison)

Status: Workstream A landed; B and C planned.

A code comparison against [nanocodex](https://github.com/gakonst/nanocodex) — a
deliberately small, library-first Rust agents SDK — surfaced three improvements
worth adopting here, without giving up what makes go-code different (a local
product with a service API, multi-provider routing, approvals, persistence).

## Workstream A — Typed tool registration (landed)

Nanocodex's `#[tool]` macro derives the JSON schema from a typed function
signature. Our Go equivalent is `tools.NewTyped` / `tools.MustTyped`
(`internal/harness/tools/typed.go`): define a tool as a typed function over an
args struct and the Parameters schema is derived by reflection from the
struct's fields and tags (`json` name + omitempty/pointer optionality, `desc`,
`min`/`max`, `enum`). The schema and the decode target can no longer drift
apart, and per-tool boilerplate (unmarshal, error wrapping, result
marshalling) disappears.

Pilot migrations: `glob` and `git_status`. Schema parity with the previous
hand-written literals is locked in by `typed_parity_test.go` (canonical-JSON
comparison), alongside behavior tests. Remaining hand-written tools migrate
opportunistically — `NewTyped` wraps the existing `Tool`/`Handler` types, so
both styles coexist and the registry, `ParallelSafe`/`Mutating` flags, policy
wrapping, and approval machinery are untouched.

## Workstream B — Split `internal/harness` (planned)

`internal/harness` is a single ~100k-line package with 124 root files; its
types file imports forensics, observational memory, store, and working memory
directly. Nanocodex's five-crate layout (dependency-light core / service /
tools / mcp / facade) is the model. Plan:

1. **Inventory PR (no code moves):** script the intra-package dependency graph
   and record the target package map here. Candidate clusters from the file
   layout: `event` (events.go), `permission` (permission_rules, plan_mode),
   `convstore` (conversation_store*, sqlite), `broker` (approval / ask-user /
   checkpoint brokers), `registry` (registry.go + typed registration), with
   the runner loop remaining in `harness`.
2. **Extract leaf-first, one cluster per PR, using type aliases** (`type X =
   newpkg.X` left in `package harness`) so the ~30 importing packages are
   untouched in the move PR. Each PR is small, CI-green, merged same day —
   never a long-lived split branch (`main` squash-merges concurrently).
3. **Importer-update PRs** then retire the aliases cluster by cluster.
4. **Guardrail:** a lint or test asserting the extracted core types package
   imports nothing heavier than stdlib, so the boundary stays true.

Verification per PR: `go build ./internal/... ./cmd/...`, the key-free smoke
(`go test ./internal/server/... -run TestRunSmoke`), and
`scripts/test-regression.sh` before merge.

## Workstream C — Public Go client (planned, after B's events extraction)

An embeddable story shaped like nanocodex's three-line hello world, but honest
to our architecture: a typed client over the existing HTTP/SSE API rather than
an in-process runner.

1. Graduate the extracted event/run-request wire types to a public package
   (e.g. `pkg/gocode/wire`) that `internal/server` itself imports — a single
   source of truth, no drift, no parity tests to maintain. This is why C waits
   for B's events extraction.
2. Build `pkg/gocode`: a `Client` with `StartRun`, `Continue`, `Cancel`, and
   an `Events(ctx, runID)` SSE iterator returning typed events. Marked
   v0/experimental.
3. Integration-test against the fake provider (`HARNESS_PROVIDER=fake`).
4. Add a short Go example to README's "Build on the API" section.

## Sequencing

A ships first (this PR). B1 (inventory) and B2 (events extraction) next; C
starts once B2 lands; remaining B extractions continue in parallel with C.
