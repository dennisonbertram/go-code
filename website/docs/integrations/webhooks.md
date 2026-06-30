---
title: "GitHub, Slack, and Linear Webhooks"
sidebar_label: "Webhooks (GitHub/Slack/Linear)"
sidebar_position: 5
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

The harness server exposes three HMAC-verified webhook endpoints — one each for GitHub, Slack, and Linear. Each endpoint converts inbound platform events into agent run actions (start a new run, steer a running one, or continue a completed one) without requiring any glue code beyond setting a single environment variable.

A fourth, source-agnostic endpoint (`POST /v1/external/trigger`) accepts the normalized envelope directly and is useful for custom sources or for triggering a Slack-sourced `start` action (see [Starting a run from Slack](#starting-a-run-from-slack)).

## Three signed endpoints

| Endpoint | Auth mechanism | Enabling env var |
|---|---|---|
| `POST /v1/webhooks/github` | HMAC-SHA256 (`X-Hub-Signature-256`) | `GITHUB_WEBHOOK_SECRET` |
| `POST /v1/webhooks/slack` | HMAC-SHA256 (`X-Slack-Signature` + timestamp) | `SLACK_SIGNING_SECRET` |
| `POST /v1/webhooks/linear` | HMAC-SHA256 raw hex (`X-Linear-Signature`) | `LINEAR_WEBHOOK_SECRET` |
| `POST /v1/external/trigger` | HMAC-SHA256 via `ValidatorRegistry` | any of the above |

**Bearer token auth is bypassed** on all four endpoints. Authentication is performed exclusively via the HMAC signature carried in the platform-specific header (or the `X-Trigger-Signature` header for the generic endpoint). Setting the corresponding environment variable both enables the endpoint and registers the validator.

When the environment variable is absent (or the adapter is not configured), the endpoint responds with `401`.

```bash
# Enable all three webhook sources at startup
GITHUB_WEBHOOK_SECRET=ghsecret123 \
SLACK_SIGNING_SECRET=slacksecret456 \
LINEAR_WEBHOOK_SECRET=linearsecret789 \
go run ./cmd/harnessd
```

## Signature schemes

Each platform uses a slightly different HMAC-SHA256 convention. The harness implements each scheme verbatim.

<Tabs>
<TabsList>
  <TabsTrigger value="github">GitHub</TabsTrigger>
  <TabsTrigger value="slack">Slack</TabsTrigger>
  <TabsTrigger value="linear">Linear</TabsTrigger>
</TabsList>
<TabsContent value="github">

**Header**: `X-Hub-Signature-256`

**Format**: `sha256=<hex>`

The harness computes `HMAC-SHA256(GITHUB_WEBHOOK_SECRET, rawBody)` and constant-time compares the result (prefixed `sha256=`) against the header value.

```go
// From internal/trigger/validator.go
mac := hmac.New(sha256.New, []byte(v.Secret))
mac.Write(env.RawBody)
expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
```

</TabsContent>
<TabsContent value="slack">

**Headers**: `X-Slack-Signature` and `X-Slack-Request-Timestamp`

**Format**: `v0=<hex>` (signature) plus a Unix-seconds timestamp

Slack's scheme hashes a base string of the form `v0:{timestamp}:{rawBody}`. The harness enforces a **5-minute freshness window** — requests older or newer than 300 seconds are rejected with `401`.

The adapter packs both headers into a single envelope field as `"<timestamp>:v0=<hex>"`, which the `SlackValidator` then splits and verifies:

```go
// From internal/trigger/validator.go
basestring := fmt.Sprintf("v0:%d:%s", ts, string(env.RawBody))
mac := hmac.New(sha256.New, []byte(v.Secret))
mac.Write([]byte(basestring))
expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
```

</TabsContent>
<TabsContent value="linear">

**Header**: `X-Linear-Signature`

**Format**: raw hex HMAC-SHA256 (no prefix)

Linear sends only the hex-encoded digest with no scheme prefix. The harness computes `HMAC-SHA256(LINEAR_WEBHOOK_SECRET, rawBody)` and constant-time compares against the raw hex value.

```go
// From internal/trigger/validator.go
mac := hmac.New(sha256.New, []byte(v.Secret))
mac.Write(env.RawBody)
expected := hex.EncodeToString(mac.Sum(nil))
```

</TabsContent>
</Tabs>

### Required headers per source

| Source | Required headers |
|---|---|
| GitHub | `X-GitHub-Event`, `X-GitHub-Delivery`, `X-Hub-Signature-256` |
| Slack | `X-Slack-Request-Timestamp`, `X-Slack-Signature` |
| Linear | `X-Linear-Signature` (optional — absence yields `401`, not `400`) |

For GitHub and Slack, missing a required header returns `400`. For Linear, a missing or invalid `X-Linear-Signature` returns `401`; Linear's `400` responses indicate an unsupported event type or unrecognized action.

## Supported event types and action mapping

The adapter for each source inspects the event type and action field to derive one of three trigger actions: `start`, `steer`, or `continue`. Unsupported combinations return `400` (empty action derived).

### GitHub

Supported event types: `issues`, `issue_comment`, `pull_request`, `pull_request_review`.

| Event type | GitHub action | Trigger action |
|---|---|---|
| `issues` | `opened`, `labeled` | `start` |
| `issue_comment` | `created` | `steer` |
| `pull_request` | `opened` | `start` |
| `pull_request` | `synchronize` | `steer` |
| `pull_request_review` | `submitted` | `steer` |

The `ThreadID` is the issue or PR number as a decimal string. Combined with `repo_owner` and `repo_name`, this produces a stable conversation identity across all events on the same issue or PR.

### Slack

Supported outer envelope type: `event_callback` only.

Inner event types `app_mention` and `message` both pass through successfully; the parser does not filter by inner type.

| Outer type | Trigger action |
|---|---|
| `event_callback` | `steer` (always) |

<Callout type="warning">
**Slack always steers — it cannot start a new run via the webhook.** All Slack `event_callback` events produce `Action = "steer"`, which requires an existing run for the derived thread. To start a new Slack-sourced run, use `POST /v1/external/trigger` with `"action": "start"` (see [Starting a run from Slack](#starting-a-run-from-slack)).
</Callout>

<Callout type="warning">
**Slack `url_verification` is not handled.** During initial Slack app setup, Slack sends a `url_verification` challenge that expects an immediate echo response. The webhook handler rejects any non-`event_callback` payload with `400`. You must complete the URL verification through a separate mechanism (e.g., a temporary HTTP handler) before pointing your Slack app at this endpoint.
</Callout>

### Linear

Supported event types: `Issue`, `Comment`.

| Event type | Linear action | Trigger action |
|---|---|---|
| `Issue` | `create` | `start` |
| `Issue` | `update` | `steer` |
| `Comment` | `create` | `steer` |

The `ThreadID` is the issue identifier (e.g. `ENG-123`) when available, falling back to the internal issue UUID. This ensures all events on the same Linear issue map to the same harness conversation.

## Action routing and thread mapping

When the harness receives a verified webhook, it:

1. Derives a **stable `ExternalThreadID`** from the source, repo owner/name, and thread ID using a SHA256 hash — so the same issue or thread always maps to the same harness conversation, regardless of which event fires.
2. Routes to one of three dispatch paths based on `Action`:

| Action | Behavior |
|---|---|
| `start` | Always starts a new run |
| `steer` | Injects a message into an existing `running`, `queued`, or `waiting_for_user` run |
| `continue` | Starts a new run continuing from a completed or failed run |

A `steer` or `continue` action with no matching run returns `404`. A `steer` against a run in the wrong state (e.g., already completed) returns `409`.

### Response codes

All three webhook endpoints share the same response code semantics:

| Code | Meaning |
|---|---|
| `202` | Request accepted and routed |
| `400` | Missing required headers, unsupported event type, or empty derived action |
| `401` | Missing/invalid signature, or adapter not configured |
| `404` | `steer`/`continue` action but no existing run for this thread |
| `409` | Run state mismatch (e.g., steering a completed run) |
| `501` | Run persistence (`Store`) not configured |

## Starting a run from Slack

Because the Slack webhook always derives `steer`, use the generic `POST /v1/external/trigger` endpoint when you need to start a new run from a Slack event. Build the envelope yourself (without a `signature` field), compute the Slack HMAC over that exact body, and send it in the `X-Trigger-Signature` header:

```bash
TIMESTAMP=$(date +%s)
BODY='{"source":"slack","source_id":"manual-001","thread_id":"C01234567:1234567890.000000","action":"start","message":"Run the nightly eval suite"}'

# Compute the Slack HMAC: HMAC-SHA256("v0:{timestamp}:{body}", SLACK_SIGNING_SECRET)
# The HMAC is computed over BODY *before* any signature field is added.
SIG=$(printf "v0:%s:%s" "$TIMESTAMP" "$BODY" \
  | openssl dgst -sha256 -hmac "$SLACK_SIGNING_SECRET" -hex \
  | awk '{print "v0="$2}')

# Send the packed timestamp:sig in the X-Trigger-Signature header, NOT in the body.
# Including the signature inside the JSON body would change the bytes being hashed
# and cause every HMAC check to fail with 401.
curl -s -X POST http://localhost:8080/v1/external/trigger \
  -H "Content-Type: application/json" \
  -H "X-Trigger-Signature: ${TIMESTAMP}:${SIG}" \
  -d "$BODY"
```

<Callout type="warning">
Always send the signature in the `X-Trigger-Signature` header, never in the JSON body. The server computes HMAC over the full raw request body (`env.RawBody`). If you embed the signature field inside the JSON body, the bytes being hashed differ from the bytes you signed, and validation returns `401`. The header is the only channel where the signature value does not change what is being hashed.

`HARNESS_AUTH_DISABLED` only disables Bearer-token middleware and does not waive signature validation on webhook or trigger routes. To test without a real Slack app you must set `SLACK_SIGNING_SECRET` and supply a matching HMAC signature in the `X-Trigger-Signature` header.
</Callout>

## Enabling webhooks: step-by-step

<Steps>
<Step>
### Set the signing secret environment variable

Set one or more of `GITHUB_WEBHOOK_SECRET`, `SLACK_SIGNING_SECRET`, or `LINEAR_WEBHOOK_SECRET` before starting `harnessd`. Each non-empty secret registers the corresponding adapter and validator automatically.

```bash
export GITHUB_WEBHOOK_SECRET="your-github-secret"
go run ./cmd/harnessd
```
</Step>
<Step>
### Configure run persistence

The `steer` and `continue` actions require a run store to look up existing runs. Set `HARNESS_RUN_DB` to a SQLite path:

```bash
export HARNESS_RUN_DB=".harness/runs.db"
```

Without persistence, routing returns `501`.
</Step>
<Step>
### Point the platform at your endpoint

Register your server's public URL in the platform's webhook settings:

- **GitHub**: Repository or organization Settings → Webhooks → Add webhook. Set the Payload URL to `https://your-server/v1/webhooks/github`. Choose `application/json` content type and paste your secret.
- **Slack**: App configuration → Event Subscriptions → Request URL: `https://your-server/v1/webhooks/slack`. Add the `app_mention` or `message` event subscriptions.
- **Linear**: Workspace Settings → API → Webhooks → Create webhook. Set the URL to `https://your-server/v1/webhooks/linear` and paste your secret.
</Step>
<Step>
### Verify with a test event

Each platform provides a "send test event" button. A successful delivery returns HTTP `202`. If you see `401`, double-check that the secret in the platform matches `GITHUB_WEBHOOK_SECRET` / `SLACK_SIGNING_SECRET` / `LINEAR_WEBHOOK_SECRET` exactly.
</Step>
</Steps>

## Example payloads

### GitHub issues event

```json
{
  "action": "opened",
  "issue": {
    "number": 42,
    "title": "Agent fails on empty input",
    "body": "Steps to reproduce: send an empty prompt."
  },
  "repository": {
    "name": "myrepo",
    "owner": { "login": "myorg" }
  }
}
```

With `X-GitHub-Event: issues` and `action: opened`, this produces trigger action `start`.

### Slack `event_callback`

```json
{
  "type": "event_callback",
  "event_id": "Ev123ABC",
  "team_id": "T012AB3C4",
  "event": {
    "type": "app_mention",
    "user": "U012AB3C4",
    "text": "<@UBOT123> do something useful",
    "ts": "1234567890.123456",
    "thread_ts": "1234567890.000000",
    "channel": "C01234567"
  }
}
```

This produces trigger action `steer` with thread ID derived from `C01234567:1234567890.000000`.

### Linear issue created

```json
{
  "type": "Issue",
  "action": "create",
  "organizationId": "org-uuid",
  "data": {
    "id": "issue-uuid",
    "identifier": "ENG-123",
    "title": "Add dark mode support",
    "description": "Users have requested a dark mode.",
    "teamId": "team-uuid"
  }
}
```

With `action: create`, this produces trigger action `start` with thread ID `ENG-123`.

## Next steps

- See [External Trigger API](/docs/reference/http-routes) for the full `POST /v1/external/trigger` request schema and all response codes.
- See [Runs and Events](/docs/concepts/events) for the SSE event stream you can subscribe to once a run starts.
- To schedule recurring agent runs on a cron expression instead of reacting to platform events, see [Cron Scheduling](/docs/integrations/cron-scheduling).
