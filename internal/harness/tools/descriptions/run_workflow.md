Run or resume a registered Go-authored workflow.

By default this tool waits for the workflow to finish and returns:
- run id
- status
- result JSON
- error, if any
- structured workflow feedback events

Workflow feedback includes progress, findings, warnings, questions, logs, and
phase changes emitted by the workflow through the SDK. Use this feedback to
reason about intermediate work instead of waiting for only a final answer.

Set `wait=false` to start the workflow and poll through the workflow run API.
Use `resume_run_id` to retry a failed workflow run with new args.
