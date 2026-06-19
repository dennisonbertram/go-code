package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	githubadapter "go-agent-harness/internal/github"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/deferred"
	"go-agent-harness/internal/harness/tools/recipe"
	linearadapter "go-agent-harness/internal/linear"
	"go-agent-harness/internal/provider/catalog"
	slackadapter "go-agent-harness/internal/slack"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/subagents"
	"go-agent-harness/internal/trigger"
)

// CronClient is the interface the HTTP server uses to manage cron jobs.
// It mirrors tools.CronClient to allow easy wiring without import complexity.
type CronClient interface {
	CreateJob(ctx context.Context, req tools.CronCreateJobRequest) (tools.CronJob, error)
	ListJobs(ctx context.Context) ([]tools.CronJob, error)
	GetJob(ctx context.Context, id string) (tools.CronJob, error)
	UpdateJob(ctx context.Context, id string, req tools.CronUpdateJobRequest) (tools.CronJob, error)
	DeleteJob(ctx context.Context, id string) error
	ListExecutions(ctx context.Context, jobID string, limit, offset int) ([]tools.CronExecution, error)
	Health(ctx context.Context) error
}

// SkillManager is the interface the HTTP server uses to query and verify skills.
// It mirrors tools.SkillVerifier to allow easy wiring without import complexity.
type SkillManager interface {
	GetSkill(name string) (tools.SkillInfo, bool)
	ListSkills() []tools.SkillInfo
	ResolveSkill(ctx context.Context, name, args, workspace string) (string, error)
	GetSkillFilePath(name string) (string, bool)
	UpdateSkillVerification(ctx context.Context, name string, verified bool, verifiedAt time.Time, verifiedBy string) error
}

func New(runner *harness.Runner) http.Handler {
	return NewWithCatalog(runner, nil)
}

// NewWithCatalog creates an HTTP handler with an optional model catalog.
// When catalog is non-nil, the GET /v1/models endpoint returns the catalog contents.
func NewWithCatalog(runner *harness.Runner, cat *catalog.Catalog) http.Handler {
	return NewWithOptions(ServerOptions{Runner: runner, Catalog: cat})
}

// ServerOptions holds the full set of optional dependencies for the HTTP server.
type ServerOptions struct {
	Runner            *harness.Runner
	Catalog           *catalog.Catalog
	AgentRunner       agentRunnerIface
	ForkedAgentRunner forkedAgentRunnerIface
	SkillLister       skillListerIface
	CronClient        CronClient
	Skills            SkillManager
	Todos             deferred.TodoManager
	Recipes           []recipe.Recipe
	Workflows         workflowManager
	ScriptWorkflows   scriptWorkflowManager
	Networks          networkManager
	Sourcegraph       sourcegraphConfig
	HTTPClient        *http.Client
	MCPConnector      MCPConnector
	SubagentManager   subagents.Manager
	ProviderRegistry  *catalog.ProviderRegistry
	Checkpoints       checkpointManager
	// Store is an optional persistence layer for run state.
	// When provided, GET /v1/runs supports filtering and completed runs are
	// retrievable after the runner forgets them.
	Store store.Store
	// AuthDisabled skips Bearer token authentication for all requests (issue #9).
	// Set to true in tests that do not provision API keys.
	AuthDisabled bool
	// ApprovalBroker is the broker for POST /v1/runs/{id}/approve and
	// POST /v1/runs/{id}/deny. When nil, those endpoints return 501.
	ApprovalBroker harness.ApprovalBroker
	// ProfilesProject is the project-level profiles directory for GET /v1/profiles.
	// Defaults to .harness/profiles relative to cwd when empty.
	ProfilesProject string
	// ProfilesUser is the user-global profiles directory for GET /v1/profiles.
	// Defaults to ~/.harness/profiles when empty.
	ProfilesUser string
	// ProfilesDir is the directory for user-created profiles.
	// When non-empty, POST/PUT/DELETE /v1/profiles/{name} endpoints are enabled.
	ProfilesDir string
	// Validators is an optional registry of webhook signature validators for
	// POST /v1/external/trigger. When nil, the endpoint returns 401 for all requests.
	Validators *trigger.ValidatorRegistry
	// GitHubAdapter is an optional GitHub webhook adapter for POST /v1/webhooks/github.
	// When nil, the endpoint returns 401 for all requests.
	GitHubAdapter *githubadapter.GitHubAdapter
	// SlackAdapter is an optional Slack webhook adapter for POST /v1/webhooks/slack.
	// When nil, the endpoint returns 401 for all requests.
	SlackAdapter *slackadapter.SlackAdapter
	// LinearAdapter is an optional Linear webhook adapter for POST /v1/webhooks/linear.
	// When nil, the endpoint returns 401 for all requests.
	LinearAdapter *linearadapter.LinearAdapter
}

// NewWithOptions creates an HTTP handler with the full set of optional dependencies.
func NewWithOptions(opts ServerOptions) http.Handler {
	s := &Server{
		runner:            opts.Runner,
		catalog:           opts.Catalog,
		providerRegistry:  opts.ProviderRegistry,
		agentRunner:       opts.AgentRunner,
		forkedAgentRunner: opts.ForkedAgentRunner,
		skillLister:       opts.SkillLister,
		cronClient:        opts.CronClient,
		skills:            opts.Skills,
		todos:             opts.Todos,
		recipes:           opts.Recipes,
		workflows:         opts.Workflows,
		scriptWorkflows:   opts.ScriptWorkflows,
		networks:          opts.Networks,
		sourcegraph:       opts.Sourcegraph,
		httpClient:        opts.HTTPClient,
		mcpConnector:      opts.MCPConnector,
		subagentManager:   opts.SubagentManager,
		checkpoints:       opts.Checkpoints,
		runStore:          opts.Store,
		approvalBroker:    opts.ApprovalBroker,
		profilesDir:       opts.ProfilesDir,
		mcpServers:        make(map[string]connectedMCPServer),
		timeNow:           time.Now,
		authDisabled:      opts.AuthDisabled || authDisabledFromEnv(),
		profilesProject:   opts.ProfilesProject,
		profilesUser:      opts.ProfilesUser,
		validators:        opts.Validators,
		githubAdapter:     opts.GitHubAdapter,
		slackAdapter:      opts.SlackAdapter,
		linearAdapter:     opts.LinearAdapter,
	}
	// If runner config has an approval broker, use it as default when none
	// is explicitly supplied in ServerOptions.
	if s.approvalBroker == nil && opts.Runner != nil {
		if ab := opts.Runner.ApprovalBroker(); ab != nil {
			s.approvalBroker = ab
		}
	}
	return s.buildMux()
}

// NewWithCron creates a Server with an optional cron client.
func NewWithCron(runner *harness.Runner, cat *catalog.Catalog, cronClient CronClient) *Server {
	return &Server{runner: runner, catalog: cat, cronClient: cronClient, mcpServers: make(map[string]connectedMCPServer), timeNow: time.Now}
}

// NewWithSkills creates a Server with an optional skill manager.
func NewWithSkills(runner *harness.Runner, cat *catalog.Catalog, skills SkillManager) *Server {
	return &Server{runner: runner, catalog: cat, skills: skills, mcpServers: make(map[string]connectedMCPServer), timeNow: time.Now}
}

// Handler returns an http.Handler for the server.
func (s *Server) Handler() http.Handler {
	return s.buildMux()
}

func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)

	// auth wraps a handler with Bearer token authentication.
	auth := s.authMiddleware

	// read wraps a handler requiring runs:read scope (after auth).
	// Combine as: auth(read(handler)) — auth runs first, then scope check.
	read := s.requireScope(store.ScopeRunsRead)
	write := s.requireScope(store.ScopeRunsWrite)
	admin := s.requireScope(store.ScopeAdmin)

	// /v1/runs  — GET requires runs:read, POST requires runs:write.
	// The handler dispatches internally so scope is enforced per-method inside
	// handleRuns / handleRunByID.
	s.registerRunRoutes(mux, auth)

	// /v1/conversations/ — mixed methods; scope enforced inside handler.
	s.registerConversationRoutes(mux, auth)

	// POST /v1/agents — requires runs:write (agent execution is a mutating operation).
	mux.Handle("/v1/agents", auth(write(http.HandlerFunc(s.handleAgents))))

	// /v1/subagents — GET requires runs:read, POST requires runs:write.
	mux.Handle("/v1/subagents", auth(http.HandlerFunc(s.handleSubagents)))
	mux.Handle("/v1/subagents/", auth(http.HandlerFunc(s.handleSubagentByID)))

	s.registerCatalogRoutes(mux, auth, read, write, admin)

	// /v1/cron — mixed methods; scope enforced inside handler.
	mux.Handle("/v1/cron/jobs", auth(http.HandlerFunc(s.handleCronJobsRoot)))
	mux.Handle("/v1/cron/jobs/", auth(http.HandlerFunc(s.handleCronJobByID)))

	// /v1/skills — GET requires runs:read; POST /verify requires runs:write.
	mux.Handle("/v1/skills", auth(read(http.HandlerFunc(s.handleSkillsRoot))))
	mux.Handle("/v1/skills/", auth(http.HandlerFunc(s.handleSkillByName)))

	// Pure read endpoints.
	mux.Handle("/v1/recipes", auth(read(http.HandlerFunc(s.handleRecipes))))
	mux.Handle("/v1/recipes/", auth(read(http.HandlerFunc(s.handleRecipes))))
	s.registerWorkflowRoutes(mux, auth)
	s.registerScriptWorkflowRoutes(mux, auth)
	s.registerNetworkRoutes(mux, auth)
	s.registerCheckpointRoutes(mux, auth)
	// POST /v1/search/code — requires runs:write (executes a search, proxying external service).
	mux.Handle("/v1/search/code", auth(write(http.HandlerFunc(s.handleSearchCode))))

	// /v1/mcp/servers — GET requires runs:read; POST/DELETE require admin.
	mux.Handle("/v1/mcp/servers", auth(http.HandlerFunc(s.handleMCPServers)))

	// /v1/profiles/ — POST requires runs:write; PUT/DELETE require runs:write.
	// GET /v1/profiles and GET /v1/profiles/{name} are read-only (runs:read).
	mux.Handle("/v1/profiles", auth(read(http.HandlerFunc(s.handleProfilesRoot))))
	mux.Handle("/v1/profiles/", auth(http.HandlerFunc(s.handleProfileByName)))

	// POST /v1/external/trigger — source-agnostic external trigger endpoint (issue #411).
	// Authentication is performed via source-specific HMAC signature validation rather
	// than the standard Bearer token middleware, so this route bypasses auth middleware.
	mux.HandleFunc("/v1/external/trigger", s.handleExternalTrigger)

	// POST /v1/webhooks/github — GitHub-specific webhook endpoint (issue #412).
	// Reads X-GitHub-Event / X-GitHub-Delivery / X-Hub-Signature-256 headers and
	// converts the GitHub payload into a normalized trigger envelope. Authentication
	// is performed via HMAC-SHA256 validation, so this route also bypasses Bearer auth.
	mux.HandleFunc("/v1/webhooks/github", s.handleGitHubWebhook)

	// POST /v1/webhooks/slack — Slack-specific webhook endpoint (issue #413).
	// Reads X-Slack-Request-Timestamp / X-Slack-Signature headers and converts the
	// Slack event_callback payload into a normalized trigger envelope. Authentication
	// is performed via HMAC-SHA256 validation, so this route also bypasses Bearer auth.
	mux.HandleFunc("/v1/webhooks/slack", s.handleSlackWebhook)

	// POST /v1/webhooks/linear — Linear-specific webhook endpoint (issue #413).
	// Reads X-Linear-Signature header and converts the Linear webhook payload into a
	// normalized trigger envelope. Authentication is performed via HMAC-SHA256 validation,
	// so this route also bypasses Bearer auth.
	mux.HandleFunc("/v1/webhooks/linear", s.handleLinearWebhook)

	return mux
}

type Server struct {
	runner            *harness.Runner
	catalog           *catalog.Catalog
	providerRegistry  *catalog.ProviderRegistry
	agentRunner       agentRunnerIface
	forkedAgentRunner forkedAgentRunnerIface
	skillLister       skillListerIface
	cronClient        CronClient
	skills            SkillManager
	approvalBroker    harness.ApprovalBroker
	checkpoints       checkpointManager

	// Todos management (issue #148)
	todos deferred.TodoManager

	// Recipe listing (issue #147)
	recipes   []recipe.Recipe
	workflows       workflowManager
	scriptWorkflows scriptWorkflowManager
	networks        networkManager

	// Sourcegraph proxy (issue #150)
	sourcegraph sourcegraphConfig
	httpClient  *http.Client

	// MCP server management (issue #145)
	mcpConnector MCPConnector
	mcpMu        sync.RWMutex
	mcpServers   map[string]connectedMCPServer

	subagentManager subagents.Manager

	// runStore is an optional persistence layer for run state (issue #7).
	// When non-nil, GET /v1/runs supports filtering and run history survives restarts.
	runStore store.Store

	// profilesDir is the directory for user-created profiles (issue #378).
	// When non-empty, POST/PUT/DELETE /v1/profiles/{name} endpoints are enabled.
	profilesDir string

	timeNow func() time.Time // injectable for tests; defaults to time.Now

	// authDisabled disables Bearer token auth for all requests (issue #9).
	authDisabled bool

	// profilesProject and profilesUser are the directories used by GET /v1/profiles.
	profilesProject string
	profilesUser    string

	// validators is the registry of webhook signature validators for
	// POST /v1/external/trigger (issue #411).
	validators *trigger.ValidatorRegistry

	// githubAdapter converts GitHub webhook requests into trigger envelopes (issue #412).
	// When nil, POST /v1/webhooks/github returns 401.
	githubAdapter *githubadapter.GitHubAdapter

	// slackAdapter converts Slack webhook requests into trigger envelopes (issue #413).
	// When nil, POST /v1/webhooks/slack returns 401.
	slackAdapter *slackadapter.SlackAdapter

	// linearAdapter converts Linear webhook requests into trigger envelopes (issue #413).
	// When nil, POST /v1/webhooks/linear returns 401.
	linearAdapter *linearadapter.LinearAdapter
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid integer: %s", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func writeSSE(w http.ResponseWriter, event harness.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %s\n", event.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "retry: 3000\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event.Type); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	return nil
}

// sseKeepaliveInterval reads HARNESS_SSE_KEEPALIVE_SECONDS from the environment
// and returns the duration. Defaults to 15 seconds.
func sseKeepaliveInterval() time.Duration {
	s := os.Getenv("HARNESS_SSE_KEEPALIVE_SECONDS")
	if s == "" {
		return 15 * time.Second
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 15 * time.Second
	}
	return time.Duration(n) * time.Second
}

// writeSSEPing writes an SSE comment line. Per the SSE spec, lines starting with
// ':' are comments that compliant clients MUST ignore. These keep connections
// alive through proxies and load balancers without affecting EventSource clients.
func writeSSEPing(w http.ResponseWriter) error {
	_, err := fmt.Fprintf(w, ": ping\n\n")
	return err
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
