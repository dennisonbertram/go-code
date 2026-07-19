# Session Rewind Runbook

Session rewind restores workspace files to the pre-image captured before a selected agent file edit, then truncates the persisted conversation after that point. It is destructive: files may be overwritten or deleted, and later conversation history is permanently removed.

## HTTP API

List points for a conversation:

```bash
curl -H "Authorization: Bearer $HARNESS_API_KEY" \
  "$HARNESS_URL/v1/conversations/$CONVERSATION_ID/rewind-points"
```

Restore a point:

```bash
curl -X POST -H "Content-Type: application/json" \
  -H "Authorization: Bearer $HARNESS_API_KEY" \
  -d '{"point_id":"<point-id>"}' \
  "$HARNESS_URL/v1/conversations/$CONVERSATION_ID/rewind"
```

`GET /rewind-points` is read-only. `POST /rewind` is the only destructive route. Include `"force": true` only when intentionally discarding an external modification: normal restore compares the on-disk file to the agent’s recorded post-edit hash and refuses a mismatch.

## TUI

- `/rewind` fetches and displays the current session’s available rewind points.
- `/rewind <point-id> confirm` submits the destructive restore request.

The confirmation token is required. Before confirming, ensure uncommitted work made outside the agent is saved elsewhere. Rewind recreates prior files and deletes files that the selected agent edit originally created; it also truncates messages after the chosen point.

## Storage and troubleshooting

Snapshots are captured before addressable `write`, `edit`, and `apply_patch` targets. Files over the per-file cap and points exceeding the per-conversation cap are listed as skipped and cannot be restored. Snapshot records are deleted automatically when their conversation is deleted or removed by retention.

If restore returns an external-modification refusal, inspect or commit the current file first; use `force` only when losing that current content is intentional.
