Deploy a project to a cloud platform (Railway, Fly.io, Cloudflare, Vercel, etc.) or check its status and logs.

Actions:
- deploy: Push the current project to the platform. Returns deployment URL and logs.
- status: Check the current deployment state (running, building, failed, sleeping).
- logs: Retrieve recent deployment or application logs.
- detect: Auto-detect the platform from workspace config files.

Parameters:
- platform: Platform adapter to use ("railway", "flyio"). If omitted, auto-detects from workspace.
- action: One of "deploy", "status", "logs", "detect" (required).
- workspace: Path to the project directory, relative to the workspace root (absolute paths must lie inside it). Defaults to the workspace root; paths outside it are rejected.
- environment: Target environment such as "staging" or "production" (default: "production").
- dry_run: Preview the deploy command without executing (default: false).
- force: Skip pre-deploy checks (default: false).

The tool wraps the platform CLI (railway, fly) and returns structured JSON results.
