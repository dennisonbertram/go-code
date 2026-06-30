import React, { useState, useCallback } from 'react';
import { Copy, Check } from 'lucide-react';
import { cn } from './ui/lib/utils';

interface PermissionsState {
  sandbox: 'unrestricted' | 'local' | 'workspace';
  approval: 'none' | 'destructive' | 'all';
}

interface FormState {
  prompt: string;
  model: string;
  provider_name: string;
  workspace_type: '' | 'local' | 'worktree' | 'container' | 'vm';
  max_steps: string;
  reasoning_effort: '' | 'low' | 'medium' | 'high';
  permissions: PermissionsState;
}

function buildRequestBody(form: FormState): Record<string, unknown> {
  const body: Record<string, unknown> = {};

  if (form.prompt) body.prompt = form.prompt;
  if (form.model) body.model = form.model;
  if (form.provider_name) body.provider_name = form.provider_name;
  if (form.workspace_type) body.workspace_type = form.workspace_type;

  const maxStepsNum = parseInt(form.max_steps, 10);
  if (form.max_steps && !isNaN(maxStepsNum) && maxStepsNum > 0) {
    body.max_steps = maxStepsNum;
  }

  if (form.reasoning_effort) body.reasoning_effort = form.reasoning_effort;

  // Always include permissions since they have meaningful defaults
  const perms: Record<string, string> = {};
  if (form.permissions.sandbox !== 'unrestricted') {
    perms.sandbox = form.permissions.sandbox;
  }
  if (form.permissions.approval !== 'none') {
    perms.approval = form.permissions.approval;
  }
  if (Object.keys(perms).length > 0) {
    body.permissions = perms;
  }

  return body;
}

function buildCurl(body: Record<string, unknown>): string {
  const json = JSON.stringify(body, null, 2);
  // Indent body lines for readability
  const indented = json.replace(/\n/g, '\n  ');
  return `curl -s -X POST http://localhost:8080/v1/runs \\\n  -H "Content-Type: application/json" \\\n  -d '${indented}'`;
}

function CopyButton({ text, className }: { text: string; className?: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [text]);

  return (
    <button
      onClick={handleCopy}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium transition-colors',
        'bg-[var(--ifm-color-emphasis-200)] hover:bg-[var(--ifm-color-emphasis-300)]',
        'text-[var(--ifm-color-emphasis-700)]',
        className,
      )}
      title="Copy to clipboard"
      type="button"
    >
      {copied ? (
        <>
          <Check className="h-3.5 w-3.5" />
          Copied
        </>
      ) : (
        <>
          <Copy className="h-3.5 w-3.5" />
          Copy
        </>
      )}
    </button>
  );
}

const labelClass = 'block text-xs font-medium mb-1 text-[var(--ifm-color-emphasis-700)]';
const inputClass =
  'w-full rounded-md border border-[var(--ifm-color-emphasis-300)] bg-[var(--ifm-background-color)] px-3 py-1.5 text-sm text-[var(--ifm-font-color-base)] placeholder:text-[var(--ifm-color-emphasis-500)] focus:outline-none focus:ring-2 focus:ring-[var(--ifm-color-primary)] focus:border-transparent transition-colors';
const selectClass = inputClass;

export default function RunRequestBuilder() {
  const [form, setForm] = useState<FormState>({
    prompt: '',
    model: 'gpt-4o',
    provider_name: '',
    workspace_type: '',
    max_steps: '',
    reasoning_effort: '',
    permissions: {
      sandbox: 'unrestricted',
      approval: 'none',
    },
  });

  const update = useCallback(
    <K extends keyof FormState>(key: K, value: FormState[K]) => {
      setForm((prev) => ({ ...prev, [key]: value }));
    },
    [],
  );

  const updatePerm = useCallback(
    <K extends keyof PermissionsState>(key: K, value: PermissionsState[K]) => {
      setForm((prev) => ({
        ...prev,
        permissions: { ...prev.permissions, [key]: value },
      }));
    },
    [],
  );

  const body = buildRequestBody(form);
  const jsonOutput = JSON.stringify(body, null, 2);
  const curlOutput = buildCurl(body);

  return (
    <div className="rounded-xl border border-[var(--ifm-color-emphasis-300)] bg-[var(--ifm-background-surface-color)] overflow-hidden my-6">
      {/* Header */}
      <div className="flex items-center gap-2 px-4 py-3 border-b border-[var(--ifm-color-emphasis-200)] bg-[var(--ifm-color-emphasis-100)]">
        <span className="inline-flex items-center rounded-md bg-blue-500/15 px-2 py-0.5 text-xs font-semibold text-blue-600 dark:text-blue-400">
          POST
        </span>
        <span className="font-mono text-sm text-[var(--ifm-font-color-base)]">
          /v1/runs
        </span>
        <span className="ml-auto text-xs text-[var(--ifm-color-emphasis-600)]">
          Request Builder
        </span>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 divide-y lg:divide-y-0 lg:divide-x divide-[var(--ifm-color-emphasis-200)]">
        {/* Form panel */}
        <div className="p-4 space-y-4">
          {/* prompt */}
          <div>
            <label className={labelClass}>
              prompt <span className="text-red-500">*</span>
            </label>
            <textarea
              className={cn(inputClass, 'min-h-[72px] resize-y')}
              placeholder="Describe the task for the agent…"
              value={form.prompt}
              onChange={(e) => update('prompt', e.target.value)}
              rows={3}
            />
          </div>

          {/* model + provider_name */}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={labelClass}>model</label>
              <input
                type="text"
                className={inputClass}
                placeholder="gpt-4o"
                value={form.model}
                onChange={(e) => update('model', e.target.value)}
              />
            </div>
            <div>
              <label className={labelClass}>provider_name</label>
              <input
                type="text"
                className={inputClass}
                placeholder="openai (optional)"
                value={form.provider_name}
                onChange={(e) => update('provider_name', e.target.value)}
              />
            </div>
          </div>

          {/* workspace_type + max_steps */}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={labelClass}>workspace_type</label>
              <select
                className={selectClass}
                value={form.workspace_type}
                onChange={(e) =>
                  update(
                    'workspace_type',
                    e.target.value as FormState['workspace_type'],
                  )
                }
              >
                <option value="">server default</option>
                <option value="local">local</option>
                <option value="worktree">worktree</option>
                <option value="container">container</option>
                <option value="vm">vm</option>
              </select>
            </div>
            <div>
              <label className={labelClass}>max_steps</label>
              <input
                type="number"
                className={inputClass}
                placeholder="unlimited"
                min={1}
                value={form.max_steps}
                onChange={(e) => update('max_steps', e.target.value)}
              />
            </div>
          </div>

          {/* reasoning_effort */}
          <div>
            <label className={labelClass}>reasoning_effort</label>
            <select
              className={selectClass}
              value={form.reasoning_effort}
              onChange={(e) =>
                update(
                  'reasoning_effort',
                  e.target.value as FormState['reasoning_effort'],
                )
              }
            >
              <option value="">provider default</option>
              <option value="low">low</option>
              <option value="medium">medium</option>
              <option value="high">high</option>
            </select>
          </div>

          {/* permissions */}
          <fieldset className="rounded-lg border border-[var(--ifm-color-emphasis-200)] p-3 space-y-3">
            <legend className="px-1 text-xs font-semibold text-[var(--ifm-color-emphasis-700)]">
              permissions
            </legend>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className={labelClass}>sandbox</label>
                <select
                  className={selectClass}
                  value={form.permissions.sandbox}
                  onChange={(e) =>
                    updatePerm(
                      'sandbox',
                      e.target.value as PermissionsState['sandbox'],
                    )
                  }
                >
                  <option value="unrestricted">unrestricted</option>
                  <option value="local">local</option>
                  <option value="workspace">workspace</option>
                </select>
              </div>
              <div>
                <label className={labelClass}>approval</label>
                <select
                  className={selectClass}
                  value={form.permissions.approval}
                  onChange={(e) =>
                    updatePerm(
                      'approval',
                      e.target.value as PermissionsState['approval'],
                    )
                  }
                >
                  <option value="none">none</option>
                  <option value="destructive">destructive</option>
                  <option value="all">all</option>
                </select>
              </div>
            </div>
          </fieldset>
        </div>

        {/* Output panel */}
        <div className="p-4 space-y-4 flex flex-col">
          {/* JSON body */}
          <div className="flex-1">
            <div className="flex items-center justify-between mb-1.5">
              <span className={labelClass + ' mb-0'}>JSON body</span>
              <CopyButton text={jsonOutput} />
            </div>
            <pre className="rounded-lg bg-[var(--ifm-code-background)] p-3 text-xs overflow-auto max-h-48 font-mono text-[var(--ifm-font-color-base)] border border-[var(--ifm-color-emphasis-200)]">
              {jsonOutput || '{}'}
            </pre>
          </div>

          {/* curl command */}
          <div className="flex-1">
            <div className="flex items-center justify-between mb-1.5">
              <span className={labelClass + ' mb-0'}>curl command</span>
              <CopyButton text={curlOutput} />
            </div>
            <pre className="rounded-lg bg-[var(--ifm-code-background)] p-3 text-xs overflow-auto max-h-48 font-mono text-[var(--ifm-font-color-base)] border border-[var(--ifm-color-emphasis-200)] whitespace-pre-wrap break-all">
              {curlOutput}
            </pre>
          </div>
        </div>
      </div>
    </div>
  );
}
