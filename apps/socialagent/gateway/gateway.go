// Package gateway wires together the Telegram bot, Postgres user store, and
// Harness HTTP client into a single HTTP handler.
package gateway

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"go-agent-harness/apps/socialagent/db"
	"go-agent-harness/apps/socialagent/harness"
	"go-agent-harness/apps/socialagent/safety"
	"go-agent-harness/apps/socialagent/systemprompt"
	"go-agent-harness/apps/socialagent/telegram"
)

// UserStore is the subset of db.Store used by the gateway.
type UserStore interface {
	GetOrCreateUser(ctx context.Context, telegramID int64, displayName string) (*db.User, error)
}

// HarnessRunner is the subset of harness.Client used by the gateway.
type HarnessRunner interface {
	SendAndWait(ctx context.Context, req harness.RunRequest) (*harness.RunResult, error)
}

// MessageSender is the subset of telegram.Bot used by the gateway.
type MessageSender interface {
	ParseUpdate(r *http.Request) (*telegram.Update, error)
	SendMessage(ctx context.Context, chatID int64, text string) error
	DisplayName(u *telegram.User) string
}

// ProfileFetcher is the subset of db.Store used to load a user's profile.
type ProfileFetcher interface {
	GetProfile(ctx context.Context, userID string) (*db.UserProfile, error)
}

// Summarizer generates and persists profile summaries from conversation history.
type Summarizer interface {
	UpdateProfile(ctx context.Context, userID, conversationID, displayName string) error
}

// ActivityLogger records user activity events.
type ActivityLogger interface {
	LogActivity(ctx context.Context, userID, displayName, activityType, content string) error
}

// SafetyChecker screens incoming user messages for harmful content before
// they are forwarded to the LLM.
type SafetyChecker interface {
	Check(ctx context.Context, message string) (*safety.Result, error)
}

// Gateway ties together the Telegram bot, user store, and harness runner.
// It serializes requests per-user so that a single conversation_id is never
// used by two concurrent harness runs.
type Gateway struct {
	bot            MessageSender
	store          UserStore
	harness        HarnessRunner
	webhookSecret  string
	profiles       ProfileFetcher
	summarizer     Summarizer
	activityLogger ActivityLogger
	mcpServerURL   string // URL of the MCP server (e.g., "http://localhost:8082/mcp")
	safety         SafetyChecker
	mu             sync.Map // map[int64]*sync.Mutex
	wg             sync.WaitGroup
	recentUpdates  sync.Map // map[int64]struct{} for deduplication
}

// NewGateway creates a Gateway.  bot, store, and harnessClient must be non-nil.
// webhookSecret is the shared secret used to authenticate incoming Telegram
// webhook requests via the X-Telegram-Bot-Api-Secret-Token header.
// profiles, sum, actLogger, and safetyChecker may be nil (features are skipped when nil).
// mcpServerURL is the URL of the MCP server; empty string disables MCP.
func NewGateway(
	bot MessageSender,
	store UserStore,
	harnessClient HarnessRunner,
	webhookSecret string,
	profiles ProfileFetcher,
	sum Summarizer,
	actLogger ActivityLogger,
	mcpServerURL string,
	safetyChecker SafetyChecker,
) *Gateway {
	return &Gateway{
		bot:            bot,
		store:          store,
		harness:        harnessClient,
		webhookSecret:  webhookSecret,
		profiles:       profiles,
		summarizer:     sum,
		activityLogger: actLogger,
		mcpServerURL:   mcpServerURL,
		safety:         safetyChecker,
	}
}

// Wait blocks until all background goroutines dispatched by HandleWebhook have
// completed.  Primarily useful in tests.
func (g *Gateway) Wait() {
	g.wg.Wait()
}

// HandleWebhook is the HTTP handler for POST /webhook/telegram.
func (g *Gateway) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != g.webhookSecret {
		w.WriteHeader(http.StatusOK)
		return
	}

	update, err := g.bot.ParseUpdate(r)
	if err != nil {
		log.Printf("gateway: parse update: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	if update.Message.From == nil {
		log.Printf("gateway: update has no From user")
		w.WriteHeader(http.StatusOK)
		return
	}

	if _, loaded := g.recentUpdates.LoadOrStore(update.UpdateID, struct{}{}); loaded {
		log.Printf("gateway: duplicate update_id=%d, skipping", update.UpdateID)
		w.WriteHeader(http.StatusOK)
		return
	}

	telegramID := update.Message.From.ID
	chatID := update.Message.Chat.ID
	text := update.Message.Text
	displayName := g.bot.DisplayName(update.Message.From)

	w.WriteHeader(http.StatusOK)

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		g.processMessage(ctx, telegramID, chatID, text, displayName)
	}()
}

// processMessage performs the actual work: per-user locking, user lookup,
// safety screening, harness call, and Telegram reply.
func (g *Gateway) processMessage(ctx context.Context, telegramID, chatID int64, text, displayName string) {
	mu := g.userMutex(telegramID)
	mu.Lock()
	defer mu.Unlock()

	user, err := g.store.GetOrCreateUser(ctx, telegramID, displayName)
	if err != nil {
		log.Printf("gateway: GetOrCreateUser(%d): %v", telegramID, err)
		g.sendError(ctx, chatID)
		return
	}

	// Screen the incoming message for harmful content before forwarding to
	// the LLM. If no safety checker is configured, skip screening.
	if g.safety != nil {
		result, err := g.safety.Check(ctx, text)
		if err != nil {
			log.Printf("gateway: safety check error (user=%d): %v", telegramID, err)
			g.sendRefusal(ctx, chatID)
			return
		}
		if !result.Safe {
			log.Printf("gateway: unsafe message blocked (user=%d, category=%s, text=%q)",
				telegramID, result.Category, text)
			g.sendRefusal(ctx, chatID)
			return
		}
	}

	renderedPrompt := g.renderSystemPrompt(ctx, user)

	req := harness.RunRequest{
		Prompt:         text,
		ConversationID: user.ConversationID,
		SystemPrompt:   renderedPrompt,
		TenantID:       user.ID,
		Permissions: &harness.PermissionConfig{
			Sandbox:  harness.SandboxScopeWorkspace,
			Approval: harness.ApprovalPolicyAll,
		},
		MaxCostUSD: 0.50,
		AllowedTools: []string{
			"compact_history",
			"context_status",
			"mcp_social_search_users",
			"mcp_social_get_user_profile",
			"mcp_social_get_updates",
			"mcp_social_save_insight",
			"mcp_social_get_my_profile",
			"mcp_social_get_community_stats",
			"mcp_social_send_message_to_user",
			"mcp_social_get_my_messages",
		},
	}
	if g.mcpServerURL != "" {
		req.MCPServers = []harness.MCPServer{{Name: "social", URL: g.mcpServerURL}}
	}

	result, err := g.harness.SendAndWait(ctx, req)
	if err != nil {
		log.Printf("gateway: SendAndWait (user=%d): %v", telegramID, err)
		g.sendError(ctx, chatID)
		return
	}

	if err := g.bot.SendMessage(ctx, chatID, result.Output); err != nil {
		log.Printf("gateway: SendMessage (chat=%d): %v", chatID, err)
	}

	if g.summarizer != nil {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			sCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := g.summarizer.UpdateProfile(sCtx, user.ID, user.ConversationID, user.DisplayName); err != nil {
				log.Printf("gateway: UpdateProfile (user=%s): %v", user.ID, err)
			}
		}()
	}

	if g.activityLogger != nil {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			aCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := g.activityLogger.LogActivity(aCtx, user.ID, user.DisplayName, "message", "Chatted with The Connector"); err != nil {
				log.Printf("gateway: LogActivity (user=%s): %v", user.ID, err)
			}
		}()
	}
}

func (g *Gateway) renderSystemPrompt(ctx context.Context, user *db.User) string {
	uctx := systemprompt.UserContext{
		DisplayName: user.DisplayName,
		UserID:      user.ID,
	}

	if g.profiles != nil {
		profile, err := g.profiles.GetProfile(ctx, user.ID)
		if err != nil {
			log.Printf("gateway: GetProfile (user=%s): %v", user.ID, err)
		} else if profile != nil {
			uctx.Summary = profile.Summary
			uctx.Interests = profile.Interests
			uctx.LookingFor = profile.LookingFor
		} else {
			uctx.IsNewUser = true
		}
	}

	rendered, err := systemprompt.Render(uctx)
	if err != nil {
		log.Printf("gateway: render system prompt: %v", err)
		return ""
	}
	return rendered
}

func (g *Gateway) userMutex(telegramID int64) *sync.Mutex {
	v, _ := g.mu.LoadOrStore(telegramID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (g *Gateway) sendError(ctx context.Context, chatID int64) {
	if err := g.bot.SendMessage(ctx, chatID, "Sorry, something went wrong. Please try again."); err != nil {
		log.Printf("gateway: sendError (chat=%d): %v", chatID, err)
	}
}

func (g *Gateway) sendRefusal(ctx context.Context, chatID int64) {
	if err := g.bot.SendMessage(ctx, chatID, safety.RefusalText); err != nil {
		log.Printf("gateway: sendRefusal (chat=%d): %v", chatID, err)
	}
}
