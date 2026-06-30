/* Hallmark · redesign: hero · genre: modern-minimal · theme: Geist (dark default)
 * nav/footer: inherited (Docusaurus) · enrichment: none (typographic + prompt install)
 * pre-emit critique: P5 H5 E4 S5 R5 V4
 */
import { useState, type ReactNode } from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import {
  Box,
  Workflow,
  GitBranch,
  Network,
  Radio,
  Route,
  Copy,
  Check,
  ArrowRight,
} from 'lucide-react';
import { FeatureGrid, FeatureCard } from '@site/src/components/ui/FeatureGrid';
import { Button } from '@site/src/components/ui/Button';

const features = [
  {
    icon: Box,
    title: 'Written in Go',
    description:
      'One self-contained binary — no Node, no Python, no virtualenv. Install it and run. Go goroutines power the concurrency underneath everything else here.',
  },
  {
    icon: Workflow,
    title: 'Parallel by design',
    description:
      'Fan out agents with the built-in workflow engine: Parallel and Pipeline primitives, schema-validated structured output, and shared token budgets, bounded by a concurrency semaphore.',
  },
  {
    icon: GitBranch,
    title: 'Isolated git worktrees',
    description:
      'Point many agents at the same repository at once. Each works in its own git worktree and branch — concurrent edits with no checkout conflicts.',
  },
  {
    icon: Network,
    title: 'Scale across machines',
    description:
      'Pool warm workspaces and push runs into Docker containers or cloud VMs. The Symphony orchestrator drives agents across them from a work queue; a relay control plane for multi-location routing is in progress.',
  },
  {
    icon: Radio,
    title: 'A runtime to build on',
    description:
      'A streamed HTTP + SSE API you can embed, script, schedule with cron, or trigger from GitHub, Slack, and Linear webhooks. Capture, replay, and diff any run.',
  },
  {
    icon: Route,
    title: 'Local-first, provider-agnostic',
    description:
      'Your machine, your keys, your repositories. Route between OpenAI, Anthropic, Google, and more — by config or live, mid-session.',
  },
];

const installCommand = 'brew install --HEAD dennisonbertram/go-code/go-code';
const driveSurfaces = ['Terminal', 'TUI', 'HTTP + SSE API', 'MCP'];

function CopyButton({ text }: { text: string }): ReactNode {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      aria-label={copied ? 'Copied' : 'Copy install command'}
      onClick={() => {
        try {
          navigator.clipboard?.writeText(text);
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1600);
        } catch {
          /* clipboard unavailable — selecting the text still works */
        }
      }}
      className="ml-1 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary active:translate-y-px"
    >
      {copied ? (
        <Check size={15} strokeWidth={2} className="text-green-500" />
      ) : (
        <Copy size={15} strokeWidth={1.75} />
      )}
    </button>
  );
}

function HeroSection(): ReactNode {
  return (
    <section className="mx-auto flex max-w-3xl flex-col items-center px-6 pb-16 pt-24 text-center sm:pt-28">
      <p className="mb-5 font-mono text-xs uppercase tracking-[0.18em] text-muted-foreground">
        Local-first · provider-agnostic
      </p>

      <h1
        className="text-balance font-semibold text-foreground"
        style={{
          fontSize: 'clamp(2.25rem, 5.5vw, 3.75rem)',
          lineHeight: 1.05,
          letterSpacing: '-0.04em',
        }}
      >
        High-performance agentic coding harness
      </h1>

      <p
        className="mt-6 text-balance text-lg leading-relaxed text-muted-foreground"
        style={{ maxWidth: '40rem' }}
      >
        A coding harness rebuilt in Go to power high-throughput, parallel agent
        runs across isolated git worktrees, containers, and machines. One static
        binary — no Node, no Python.
      </p>

      <div className="mt-9 flex flex-col items-center gap-3 sm:flex-row">
        <Link to="/docs/getting-started/what-is-go-code" className="hover:no-underline">
          <Button variant="primary" size="lg">
            Get started
            <ArrowRight size={16} strokeWidth={2} className="ml-1.5" />
          </Button>
        </Link>
        <Link to="https://github.com/dennisonbertram/go-code" className="hover:no-underline">
          <Button variant="outline" size="lg">
            View on GitHub
          </Button>
        </Link>
      </div>

      <div className="mt-8 flex w-full max-w-xl items-center gap-3 rounded-lg border border-border bg-card px-4 py-2.5 text-left">
        <span aria-hidden className="select-none font-mono text-sm text-muted-foreground">
          $
        </span>
        <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap font-mono text-sm text-foreground">
          {installCommand}
        </code>
        <CopyButton text={installCommand} />
      </div>

      <div className="mt-7 flex flex-wrap items-center justify-center gap-x-2.5 gap-y-1 font-mono text-xs text-muted-foreground">
        <span className="text-muted-foreground/70">Drive it from</span>
        {driveSurfaces.map((s) => (
          <span key={s} className="inline-flex items-center gap-2.5">
            <span aria-hidden className="text-border">
              ·
            </span>
            {s}
          </span>
        ))}
      </div>
    </section>
  );
}

const tiers = [
  {
    name: 'TypeScript & Python',
    tag: 'Where most agents live',
    examples: 'Claude Code · Cursor · Cline · Aider · OpenHands',
    body: 'Rich editor and terminal experiences, shipped as Node or Python apps — the default home for most coding agents today.',
    accent: false,
  },
  {
    name: 'Rust',
    tag: 'The compiled-performance tier',
    examples: 'Codex CLI · Goose',
    body: 'Single fast binaries — where most of the performance-first rewrites landed.',
    accent: false,
  },
  {
    name: 'Go',
    tag: "go-code's lane",
    examples: 'go-code',
    body: "A single static binary and goroutine concurrency, with Go's simplicity and a deep backend-engineering ecosystem. The lane go-code is built for.",
    accent: true,
  },
];

function LandscapeSection(): ReactNode {
  return (
    <section className="border-t border-border">
      <div className="mx-auto max-w-[1100px] px-6 py-20">
        <div className="mb-10 max-w-2xl">
          <h2 className="text-2xl font-semibold tracking-tight text-foreground">
            Where go-code fits
          </h2>
          <p className="mt-3 text-base leading-relaxed text-muted-foreground">
            The coding-agent field has clustered into TypeScript, Python, and
            Rust. go-code takes the lane it largely skipped — Go — and makes
            parallel orchestration the point.
          </p>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          {tiers.map((t) => (
            <div
              key={t.name}
              className={`flex flex-col gap-2 rounded-lg border p-6 ${
                t.accent ? 'border-primary/50 bg-primary/5' : 'border-border bg-card'
              }`}
            >
              <div className="flex items-center justify-between gap-2">
                <h3 className="text-sm font-semibold text-foreground">{t.name}</h3>
                {t.accent && (
                  <span className="rounded-full bg-primary px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-primary-foreground">
                    You are here
                  </span>
                )}
              </div>
              <p className="text-xs uppercase tracking-wider text-muted-foreground">
                {t.tag}
              </p>
              <p className="font-mono text-xs text-muted-foreground/80">
                {t.examples}
              </p>
              <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                {t.body}
              </p>
            </div>
          ))}
        </div>

        <p className="mt-8 max-w-3xl text-sm leading-relaxed text-muted-foreground">
          go-code's bet: Go's single-binary deployability and goroutine
          concurrency, plus parallel multi-agent orchestration — fan-out,
          isolated git worktrees, containers, and a streamed HTTP runtime — in
          the open.
        </p>
      </div>
    </section>
  );
}

export default function Home(): ReactNode {
  return (
    <Layout
      title="go-code — high-performance agentic coding harness"
      description="go-code is a high-performance agentic coding harness, rebuilt in Go to power high-throughput, parallel agent runs across isolated git worktrees, containers, and machines — from a single static binary, with no Node or Python."
    >
      <main>
        <HeroSection />

        <section className="mx-auto max-w-[1100px] px-6 pb-24">
          <h2 className="mb-12 text-center text-2xl font-semibold tracking-tight text-foreground">
            What makes go-code different
          </h2>
          <FeatureGrid columns={3}>
            {features.map((f) => (
              <FeatureCard
                key={f.title}
                icon={f.icon}
                title={f.title}
                description={f.description}
              />
            ))}
          </FeatureGrid>
        </section>

        <LandscapeSection />
      </main>
    </Layout>
  );
}
