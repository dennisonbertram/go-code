---
title: "Authentication and Tenancy"
sidebar_label: "Authentication"
sidebar_position: 2
---

import { Callout, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

`harnessd` protects its HTTP API with Bearer token authentication backed by a key store. Each API key carries a tenant identity and a set of permission scopes. The server validates every incoming request against those scopes before dispatching it to a handler. Multi-tenant deployments get an additional layer: a tenant isolation check that ensures one tenant cannot read or modify another's runs.

This page explains how to send authenticated requests (including the SSE fallback), what the scope hierarchy means, when auth is implicitly off, and what the isolation model guarantees.

---

## How authentication works

Every protected route goes through `authMiddleware` in `internal/server/auth.go`. The middleware extracts the raw token, validates it against the key store, and — on success — injects the authenticated tenant ID, the first eight characters of the key (used in audit trails), and the key's scopes into the request context. Downstream scope-enforcement middleware reads those values to gate individual routes.

### Token extraction order

The middleware looks for a token in two places, in this order:

1. **`Authorization: Bearer <token>` header** — the standard method for REST calls.
2. **`?token=<token>` query parameter** — the fallback for `EventSource` clients. The browser `EventSource` API does not allow setting custom headers, so passing the token on the query string lets you stream `GET /v1/runs/{id}/events` from JavaScript without a proxy.

<Callout variant="info" title="Only the query fallback is for EventSource">
The `?token=` parameter exists solely to unblock browser-based SSE clients. For all non-streaming requests prefer the `Authorization` header to avoid accidentally logging credentials in server access logs or proxies that capture query strings.
</Callout>

### Sending an authenticated request

<Tabs defaultValue="header">
  <TabsList>
    <TabsTrigger value="header">Bearer header</TabsTrigger>
    <TabsTrigger value="sse">SSE / EventSource fallback</TabsTrigger>
  </TabsList>
  <TabsContent value="header">

Standard REST call with the `Authorization` header:

```bash
TOKEN="harness_sk_yourkey"

# Start a run
curl -s -X POST http://localhost:8080/v1/runs \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"prompt": "list files in the workspace"}'

# Fetch the run status
curl -s http://localhost:8080/v1/runs/<run_id> \
  -H "Authorization: Bearer $TOKEN"
```

  </TabsContent>
  <TabsContent value="sse">

SSE stream using the `?token=` query parameter (shown here for parity with the browser `EventSource` pattern):

```bash
TOKEN="harness_sk_yourkey"
RUN_ID="run_abc123"

# Stream events — token passed as query param to match the EventSource pattern;
# curl can send the Authorization header instead if preferred
curl -N "http://localhost:8080/v1/runs/${RUN_ID}/events?token=${TOKEN}"
```

In browser JavaScript:

```typescript
const token = "harness_sk_yourkey";
const runId = "run_abc123";

const source = new EventSource(
  `http://localhost:8080/v1/runs/${runId}/events?token=${token}`
);

source.addEventListener("run.completed", (e) => {
  console.log("done", JSON.parse(e.data));
  source.close();
});
```

  </TabsContent>
</Tabs>

### Error responses

A missing or invalid token returns HTTP `401 Unauthorized`:

```json
{"error": {"code": "unauthorized", "message": "authorization required"}}
```

An expired key returns the same status with `"api key expired"` as the message. A failed scope check returns HTTP `403 Forbidden` with a structured body:

```json
{"error": "insufficient_scope", "required": "runs:write"}
```

---

## Scopes

Every API key is issued with one or more scopes. The server enforces the minimum required scope per route. Three scopes exist:

<Card>
  <CardHeader>
    <CardTitle>Scope hierarchy</CardTitle>
  </CardHeader>
  <CardContent>

| Scope | Constant | What it unlocks |
|---|---|---|
| `runs:read` | `store.ScopeRunsRead` | All `GET` routes: list runs, get run, get events, list conversations, list models, list skills, and more. |
| `runs:write` | `store.ScopeRunsWrite` | All mutation routes: `POST /v1/runs`, cancel, steer, continue, approve, deny, and all `POST`/`PUT`/`DELETE` routes. **Also satisfies `runs:read`** — a single write-scoped key can do everything. |
| `admin` | `store.ScopeAdmin` | Superscope. Satisfies any scope check, including privileged routes like `PUT /v1/providers/{name}/key` and `POST /v1/mcp/servers`. |

  </CardContent>
</Card>

The scope hierarchy is implemented in `hasScope` (`internal/server/auth.go`):

- `admin` satisfies every check unconditionally.
- `runs:write` satisfies a `runs:read` check (write implies read).
- When no scopes exist in the request context — which happens whenever auth is disabled — every scope check passes automatically, preserving the development workflow.

### Generating a key

`harnesscli auth login` generates and stores an API key locally without contacting the server. The default flags give the key all three scopes:

```bash
harnesscli auth login \
  --server http://localhost:8080 \
  --tenant default \
  --name my-cli-key
```

The command writes the server URL and raw key to `~/.harness/config.json` (mode `0600`) and prints an example `Authorization: Bearer ...` header you can copy into curl or your HTTP client configuration.

<Callout variant="info" title="Key generation is local">
`harnesscli auth login` does not call the server. It generates a key locally and saves it to disk. The key only becomes usable once it is registered in the server's key store — this step is handled separately when a persistent store (`HARNESS_RUN_DB`) is configured.
</Callout>

---

## When auth is disabled

Authentication is entirely skipped under three conditions:

1. **`HARNESS_AUTH_DISABLED=true` environment variable** — explicitly disables auth at startup. All requests are allowed through without a token.
2. **`ServerOptions.AuthDisabled = true`** — the same flag set programmatically when embedding the server in tests or custom binaries.
3. **No `Store` is configured** — when `HARNESS_RUN_DB` is not set, `harnessd` has no key store to validate against. The middleware detects a nil store and skips auth entirely, even without the explicit `HARNESS_AUTH_DISABLED` flag.

<Callout variant="warning" title="Auth is silently off without a persistent store">
If you start `harnessd` without setting `HARNESS_RUN_DB`, the server accepts every request with no token required. This is intentional for local development and key-free testing, but it is a significant footgun if you expose the port to a network. Before opening the firewall or binding to a non-loopback address, make sure `HARNESS_RUN_DB` is set and at least one API key is registered.
</Callout>

The key-free smoke test exploits condition 1 deliberately:

```bash
HARNESS_PROVIDER=fake \
HARNESS_AUTH_DISABLED=true \
  go run ./cmd/harnessd
```

This is safe for local testing because no key store is involved, and the fake provider makes no real LLM calls.

Webhook routes (`/v1/webhooks/github`, `/v1/webhooks/slack`, `/v1/webhooks/linear`, `/v1/external/trigger`) bypass Bearer auth entirely regardless of the above settings. They authenticate via HMAC signature headers instead.

---

## Tenancy

Every API key carries a `tenant_id`. The harness uses this to scope all run and conversation operations to a single tenant within a shared server instance.

### How isolation is enforced

When a request arrives with a valid token, the authenticated tenant ID is injected into the request context by `authMiddleware`. Every route that creates or retrieves a run compares the context tenant against the resource's stored tenant using `normalizeTenant`:

- If the run's tenant matches the caller's tenant, the request proceeds normally.
- If the run belongs to a different tenant, the server returns **HTTP 404**, not 403.

The 404-not-403 pattern is deliberate: it prevents resource-existence disclosure. A caller that guesses another tenant's run ID learns nothing — the response is indistinguishable from the run not existing at all.

### The `default` tenant

An empty `tenant_id` (`""`) is normalized to `"default"` for all ownership comparisons. This means:

- A run started without a `tenant_id` field is owned by the `default` tenant.
- A key with `tenant_id = ""` compares equal to a key with `tenant_id = "default"`.
- In single-tenant or dev deployments, omitting `tenant_id` everywhere works consistently.

### Tenant mismatch in run requests

When auth is enabled and you include a `tenant_id` in a `POST /v1/runs` body, that value must match the authenticated tenant of your API key. A mismatch is rejected with HTTP 400 before the run is created. If you omit `tenant_id` from the request body, the server silently fills it from the authenticated key — the most common and recommended usage.

---

## Quick reference

| Mechanism | Where | Notes |
|---|---|---|
| Bearer token | `Authorization: Bearer <token>` header | Preferred for all REST calls |
| SSE token fallback | `?token=<token>` query param | Use only for `EventSource` / streaming GETs |
| Scope: `runs:read` | GET routes | Read-only access |
| Scope: `runs:write` | POST/PUT/DELETE routes | Implies `runs:read` |
| Scope: `admin` | Privileged routes (provider keys, MCP) | Satisfies any scope check |
| Disable auth (explicit) | `HARNESS_AUTH_DISABLED=true` | Development/testing |
| Disable auth (implicit) | No `HARNESS_RUN_DB` set | Footgun in production |
| Cross-tenant response | HTTP 404 | Prevents existence disclosure |
| Empty tenant | Normalized to `"default"` | Consistent in single-tenant setups |

---

## Next steps

- See [Running the Server](/docs/server/harnessd) to learn how `harnessd` is configured and started.
- The [HTTP API reference](/docs/reference/http-routes) lists every route with its required scope.
- To understand what events flow over the SSE stream once a run is authenticated and started, see [Events](/docs/concepts/events).
