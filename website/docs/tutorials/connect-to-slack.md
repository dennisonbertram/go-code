---
title: "Tutorial: Connecting go-code to Slack"
sidebar_label: "Connect to Slack"
sidebar_position: 3
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

The Slack integration lets you point a Slack app's [Event API](https://api.slack.com/apis/events-api) webhook at your running `harnessd` instance. When Slack sends an `event_callback` (such as an `app_mention` or a `message` in a channel), the server verifies the request signature, maps the event to a persistent conversation thread, and steers an existing agent run with the message text. This gives you a Slack-native interface to your agents without writing any glue code beyond a single environment variable.

**What you need:**
- A running `harnessd` instance reachable from the public internet (or a tunnel like ngrok for local development)
- A Slack workspace where you can create an app
- `HARNESS_RUN_DB` set so runs are persisted across requests (required — see below)

<Callout variant="warning">
The Slack webhook always produces `action = "steer"`. That means it routes messages into an **existing** run — it cannot start a new run by itself. To start a Slack-sourced run, use `POST /v1/external/trigger` with `"action": "start"` (covered in the [Starting vs steering](#starting-vs-steering) section).
</Callout>

---

## Create the Slack app

<Steps>
<Step>

**Create a new app.** Go to [api.slack.com/apps](https://api.slack.com/apps), click **Create New App**, and choose **From scratch**. Give it a name and pick your workspace.

</Step>
<Step>

**Add bot scopes.** Under **OAuth & Permissions**, add the following bot token scopes:

| Scope | Why |
|-------|-----|
| `app_mentions:read` | Receive `app_mention` events |
| `chat:write` | Post replies (if your agent sends messages back) |
| `channels:history` | Read messages in public channels (for `message` events) |

Install the app to your workspace when prompted.

</Step>
<Step>

**Enable the Event API.** Under **Event Subscriptions**, toggle **Enable Events** on. You will need to provide a **Request URL** — that is `https://<your-host>/v1/webhooks/slack`. Slack sends a `url_verification` challenge to this URL during setup (see the warning below).

</Step>
<Step>

**Subscribe to bot events.** Still on the Event Subscriptions page, click **Add Bot User Event** and add:

- `app_mention` — fires when a user mentions your bot
- `message.channels` — fires on messages in channels where your bot is present (optional)

Save your changes.

</Step>
<Step>

**Copy the Signing Secret.** Under **Basic Information**, find the **App Credentials** section and copy the **Signing Secret**. You will set this as `SLACK_SIGNING_SECRET` in the next section.

</Step>
</Steps>

<Callout variant="warning">
**`url_verification` challenge.** When you paste the webhook URL into Slack's Event Subscriptions page, Slack sends a POST with `"type": "url_verification"` and expects a JSON response with the challenge value. The `harnessd` Slack endpoint only handles `event_callback` payloads — any other type returns `400`. This means Slack's URL verification check will fail if `harnessd` is the only thing listening.

**Workaround:** Route the `url_verification` challenge through a small proxy (an AWS Lambda, a Cloudflare Worker, or an nginx location block) that responds to `url_verification` directly and forwards everything else to `harnessd`. Alternatively, set up a temporary Express/Go endpoint just for the initial verification, swap it to `harnessd` once the app is saved, and re-save the URL.
</Callout>

---

## Enable the endpoint

The `/v1/webhooks/slack` endpoint is **off by default**. Setting `SLACK_SIGNING_SECRET` in the environment is the only step needed to turn it on.

```bash
# Start harnessd with Slack support and a persistent run store
export SLACK_SIGNING_SECRET=your-slack-signing-secret-here
export HARNESS_RUN_DB=/var/lib/harness/runs.db      # required — see note below
export OPENAI_API_KEY=sk-...                         # or another provider key

go run ./cmd/harnessd
```

When `SLACK_SIGNING_SECRET` is non-empty, `harnessd` registers a `SlackValidator` and a `SlackAdapter` at startup. Without it, any POST to `/v1/webhooks/slack` returns:

```json
HTTP 401
{"error": {"code": "invalid_signature", "message": "Slack webhook adapter not configured"}}
```

<Callout variant="warning">
**Persistent store required.** The Slack webhook steers an existing run — and looking up a run by thread ID requires a persistent store. If `HARNESS_RUN_DB` is not set, the endpoint returns `501 Not Implemented`. Set `HARNESS_RUN_DB` to a SQLite file path before starting `harnessd`.
</Callout>

### Exposing harnessd publicly

For local development, use [ngrok](https://ngrok.com/) to create a tunnel:

```bash
ngrok http 8080
```

Copy the `https://` URL ngrok prints and use it as your Slack webhook URL:

```
https://<random>.ngrok-free.app/v1/webhooks/slack
```

For production, deploy `harnessd` behind a reverse proxy (nginx, Caddy) with TLS, or run it directly on a host with a public IP.

---

## Signature and routing

### How Slack signs requests

Every request Slack sends includes two headers:

| Header | Description |
|--------|-------------|
| `X-Slack-Request-Timestamp` | Unix timestamp (seconds) when Slack sent the request |
| `X-Slack-Signature` | HMAC value in the format `v0=<hex>` |

`harnessd` validates the signature using this algorithm (from `internal/trigger/validator.go`):

1. Concatenate the signing base string: `"v0:" + timestamp + ":" + rawBody`
2. Compute `HMAC-SHA256(signingSecret, baseString)`
3. Encode as hex and prepend `"v0="`
4. Reject requests where the timestamp differs from `now` by more than 300 seconds (5-minute freshness window)
5. Compare the computed signature against `X-Slack-Signature` using a constant-time comparison

In shell notation:

```bash
BASE="v0:${TIMESTAMP}:${BODY}"
SIG="v0=$(printf '%s' "${BASE}" | openssl dgst -sha256 -hmac "${SIGNING_SECRET}" | awk '{print $NF}')"
```

> **Portability note:** On Linux, `openssl dgst` outputs `HMAC-SHA256(stdin)= <hex>` (two fields), so `$2` works. On macOS (LibreSSL), it outputs only the hex value (one field), so `$2` returns empty and the signature becomes `v0=`. Using `$NF` (last field) works correctly on both platforms.

### Packed signature format

Internally, the `SlackAdapter` packs both Slack headers into a single `Signature` field before handing the envelope to the validator:

```
Signature = "<X-Slack-Request-Timestamp>:<X-Slack-Signature>"
          = "1609459200:v0=abc123def456..."
```

This packed format is what `SlackValidator.ValidateSignature` expects. You do not need to format it this way when calling `POST /v1/webhooks/slack` — you set the two Slack headers normally and the adapter handles the packing.

### Thread-to-conversation mapping

Each Slack event carries a channel ID and a thread timestamp. The adapter builds a raw thread ID:

- **Threaded reply** (`thread_ts` present): `"<channelID>:<thread_ts>"`
- **Top-level message** (`thread_ts` absent): `"<channelID>:<ts>"`

The server then hashes this into a stable SHA-256-based conversation ID of the form `"slack:<hex>"`. All messages in the same Slack thread resolve to the same conversation ID, so the agent maintains context across a thread.

---

## Starting vs steering

<Callout variant="info">
Understanding this distinction is important — it determines whether you use the Slack-specific webhook or the generic trigger endpoint.
</Callout>

<Tabs defaultValue="steer">
<TabsList>
  <TabsTrigger value="steer">Steer an existing run (Slack webhook)</TabsTrigger>
  <TabsTrigger value="start">Start a new run (external trigger)</TabsTrigger>
</TabsList>

<TabsContent value="steer">

The `/v1/webhooks/slack` endpoint **always** produces `action = "steer"`. It looks up the existing run associated with the Slack thread's conversation ID and injects your message into that run.

This works when:
- A run was previously started for this Slack thread
- The run is in `running`, `queued`, or `waiting_for_user` status

If no matching run exists, the endpoint returns `404`. If the run is in a terminal state (`completed`, `failed`, `cancelled`), it returns `409`.

</TabsContent>

<TabsContent value="start">

To **start** a new run from Slack (for example, when a user opens a new thread or uses a slash command), send a signed request to `POST /v1/external/trigger` with `"action": "start"`:

```json
{
  "source":    "slack",
  "action":    "start",
  "thread_id": "C01234567:1234567890.000000",
  "message":   "Please summarize today's standup notes",
  "signature": "<packed-signature>"
}
```

The `thread_id` must be in the same `"<channelID>:<ts>"` format that the Slack adapter uses. The server derives an identical conversation ID, so subsequent steer messages from the webhook will route to the run you just started.

</TabsContent>
</Tabs>

### Testing with a signed curl

Before wiring up Slack, verify the endpoint works with a hand-crafted signed request. This exercises the full signature validation and dispatch path without needing a real Slack event.

```bash
#!/usr/bin/env bash
# Compute a valid Slack HMAC signature and POST a signed event to harnessd.
# Prerequisites: openssl, curl, a running harnessd with SLACK_SIGNING_SECRET set.

SIGNING_SECRET="your-slack-signing-secret-here"
HARNESS_URL="http://localhost:8080"

# Use the current Unix timestamp so the 5-minute freshness window passes.
TIMESTAMP=$(date +%s)

# A minimal Slack event_callback payload.
BODY='{
  "type": "event_callback",
  "event_id": "Ev001TEST",
  "team_id": "T012AB3C4",
  "event": {
    "type": "app_mention",
    "user": "U012AB3C4",
    "text": "<@UBOT123> run the daily report",
    "ts": "1234567890.123456",
    "thread_ts": "1234567890.000000",
    "channel": "C01234567"
  }
}'

# Compute the HMAC-SHA256 signature.
BASE="v0:${TIMESTAMP}:${BODY}"
SIG="v0=$(printf '%s' "${BASE}" | openssl dgst -sha256 -hmac "${SIGNING_SECRET}" | awk '{print $NF}')"

echo "Timestamp : ${TIMESTAMP}"
echo "Signature : ${SIG}"
echo ""

# Send the request.
curl -s -w "\nHTTP %{http_code}\n" \
  -X POST "${HARNESS_URL}/v1/webhooks/slack" \
  -H "Content-Type: application/json" \
  -H "X-Slack-Request-Timestamp: ${TIMESTAMP}" \
  -H "X-Slack-Signature: ${SIG}" \
  -d "${BODY}"
```

**Expected responses:**

| Response | Meaning |
|----------|---------|
| `202 Accepted` | Signature valid; run steered successfully |
| `404 Not Found` | Signature valid; no existing run for this thread (expected on first call) |
| `401 Unauthorized` | Signature invalid or adapter not configured |
| `501 Not Implemented` | `HARNESS_RUN_DB` is not set |

A `404` on the first call is normal — it means signature validation passed and the routing logic ran, but there is no existing run yet for this thread. Start a run via `POST /v1/external/trigger` with `"action": "start"` (or via `POST /v1/runs`), then re-send the curl; you should get `202`.

---

## Quick-reference: response codes

| Code | Meaning |
|------|---------|
| `202` | Event accepted and routed |
| `400` | Missing `X-Slack-Request-Timestamp` or `X-Slack-Signature` header, unsupported event type (not `event_callback`), or empty action |
| `401` | Invalid or expired signature, or `SLACK_SIGNING_SECRET` not set |
| `404` | `steer` action but no run found for this thread |
| `409` | Run is in a terminal state (completed/failed/cancelled) |
| `501` | `HARNESS_RUN_DB` not configured |

---

## Next steps

- **Start runs programmatically:** Use `POST /v1/runs` to create a run with a specific `conversation_id` that matches the Slack thread's derived ID, then let the webhook steer it.
- **Stream run output:** Subscribe to `GET /v1/runs/{id}/events` for a real-time SSE stream of the agent's progress — useful for building a Slack bot that posts updates as the agent works.
- **Connect GitHub or Linear:** The same HMAC pattern is used by `/v1/webhooks/github` (requires `GITHUB_WEBHOOK_SECRET`) and `/v1/webhooks/linear` (requires `LINEAR_WEBHOOK_SECRET`).
