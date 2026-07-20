# Codex ChatGPT-Subscription Authentication

`codex-subscription` is an additive provider that routes requests through the
ChatGPT Codex backend using an existing local `codex` CLI ChatGPT session. It
does not use `OPENAI_API_KEY`, so it is distinct from the metered `openai`
provider.

## Setup

1. Authenticate the vendor CLI: `codex login`.
2. Import a private, harness-owned copy: `harnesscli auth codex login`.
3. Verify safely: `harnesscli auth codex status`.
4. Select the provider explicitly, for example `HARNESS_PROVIDER=codex-subscription`.

The import reads `~/.codex/auth.json` once and never writes anything under
`~/.codex`. The copied credential lives at
`~/.harness/subscription-auth/codex.json`; its directory/file modes are
`0700`/`0600`. `harnesscli auth codex logout` deletes only this copied file.

On expiry, the harness refreshes its own copy with the vendor Codex OAuth
client contract and keeps the account id as the `chatgpt-account-id` request
header. `status` prints the account id and validity only; it never prints a
credential.

## Limitations and account policy

This route follows the behavior of the installed Codex CLI and targets its
ChatGPT backend rather than OpenAI's documented metered API. It is not an
official OpenAI API integration, may change without notice, and users are
responsible for ensuring their ChatGPT plan and use comply with applicable
OpenAI terms, limits, and organizational policy. Keep `openai` with an
`OPENAI_API_KEY` for the documented API route.

If no vendor credential has been imported, the provider remains unconfigured.
The remediation is always: run `codex login`, then `harnesscli auth codex
login`.
