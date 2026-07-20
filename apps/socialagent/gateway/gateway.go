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

// Screener screens user input for safety policy violations before the message
// is forwarded to the harness. When nil, all messages pass through unscreened.
type Screener interface {
	Screen(ctx context.Context, text string) (*safety.Result, error)
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
	screener       Screener // optional safety screener; nil means disabled
	mcpServerURL   string   // URL of the MCP server (e.g., "http://localhost:8082/mcp")
	mu             sync.Map // map[int64]*sync.Mutex
	wg             sync.WaitGroup
	recentUpdates  sync.Map // map[int64]struct{} for deduplication
}

// NewGateway creates a Gateway.  bot, store, and harnessClient must be non-nil.
// webhookSecret is the shared secret used to authenticate incoming Telegram
// webhook requests via the X-Telegram-Bot-Api-Secret-Token header.
// profiles, sum, actLogger, and screener may be nil (features are skipped when nil).
// mcpServerURL is the URL of the MCP server; empty string disables MCP.
func NewGateway(
	bot MessageSender,
	store UserStore,
	harnessClient HarnessRunner,
	webhookSecret string,
	profiles ProfileFetcher,
	sum Summarizer,
	actLogger ActivityLogger,
	screener Screener,
	mcpServerURL string,
) *Gateway {
	return &Gateway{
		bot:            bot,
		store:          store,
		harness:        harnessClient,
		webhookSecret:  webhookSecret,
		profiles:       profiles,
		summarizer:     sum,
		activityLogger: actLogger,
		screener:       screener,
		mcpServerURL:   mcpServerURL,
	}
}

// Wait blocks until all background goroutines dispatched by HandleWebhook have
// completed.  Primarily useful in tests.
func (g *Gateway) Wait() {
	g.wg.Wait()
}

// HandleWebhook is the HTTP handler for POST /webhook/telegram.
// It always returns 200 OK immediately — returning any other status causes
// Telegram to retry the same update indefinitely.  The actual work is
// dispatched to a background goroutine so that the HTTP response is sent
// before the (potentially long) harness call completes.
func (g *Gateway) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	// 0. Authenticate the request using the shared webhook secret.
	//    We return 200 even on auth failure to avoid leaking information to
	//    spoofed senders and to prevent Telegram retry storms if misconfigured.
	if r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != g.webhookSecret {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 1. Parse the incoming Telegram update.
	update, err := g.bot.ParseUpdate(r)
	if err != nil {
		// Not a text message or malformed JSON — acknowledge silently.
		log.Printf("gateway: parse update: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Guard against a missing From field (e.g. channel posts).
	if update.Message.From == nil {
		log.Printf("gateway: update has no From user")
		w.WriteHeader(http.StatusOK)
		return
	}

	// 2. Deduplicate: skip updates we have already dispatched.
	if _, loaded := g.recentUpdates.LoadOrStore(update.UpdateID, struct{}{}); loaded {
		log.Printf("gateway: duplicate update_id=%d, skipping", update.UpdateID)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Capture all fields needed by the goroutine before returning.
	telegramID := update.Message.From.ID
	chatID := update.Message.Chat.ID
	text := update.Message.Text
	displayName := g.bot.DisplayName(update.Message.From)

	// 3. Return 200 OK to Telegram immediately.
	w.WriteHeader(http.StatusOK)

	// 4. Process the message in the background so we don't hold the HTTP
	//    connection open for the duration of the harness call.
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		g.processMessage(ctx, telegramID, chatID, text, displayName)
	}()
}

// processMessage performs the actual work: per-user locking, user lookup,
// harness call, and Telegram reply.  It is called from a background goroutine
// and must not touch the original http.Request or ResponseWriter.
func (g *Gateway) processMessage(ctx context.Context, telegramID, chatID int64, text, displayName string) {
	// Acquire per-user mutex to prevent concurrent runs on the same conversation.
	mu := g.userMutex(telegramID)
	mu.Lock()
	defer mu.Unlock()

	// Look up (or create) the internal user record.
	user, err := g.store.GetOrCreateUser(ctx, telegramID, displayName)
	if err != nil {
		log.Printf("gateway: GetOrCreateUser(%d): %v", telegramID, err)
		g.sendError(ctx, chatID)
		return
	}

	// Screen user input for safety before forwarding to the harness.
	// When no screener is configured (nil), all messages pass through.
	if g.screener != nil {
		result, err := g.screener.Screen(ctx, text)
		if err != nil {
			log.Printf("gateway: screener.Screen: %v", err)
			// Fail-open: continue processing even if screener errors.
		} else if !result.Safe {
			log.Printf("gateway: message blocked by safety screener (category=%s, reason=%s)", result.Category, result.Reason)
			msg := safety.ParseCategory(result)
			if msg == "" {
				msg = "I'm not able to help with that request."
			}
			if sendErr := g.bot.SendMessage(ctx, chatID, msg); sendErr != nil {
				log.Printf("gateway: SendMessage (chat=%d): %v", chatID, sendErr)
			}
			return
		}
	}

	// Fetch the user's profile and render the system prompt with user context.
	renderedPrompt := g.renderSystemPrompt(ctx, user)

	// Build the run request, including MCP server config if configured.
	// AllowedTools restricts the agent to only the tools it needs for social
	// interactions — prevents access to bash, file I/O, and other sensitive
	// built-in tools from a Telegram-facing bot.
	req := harness.RunRequest{
		Prompt:         text,
		ConversationID: user.ConversationID,
		SystemPrompt:   renderedPrompt,
		TenantID:       user.ID,
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

	// Delegate to the harness.
	result, err := g.harness.SendAndWait(ctx, req)
	if err != nil {
		log.Printf("gateway: SendAndWait (user=%d): %v", telegramID, err)
		g.sendError(ctx, chatID)
		return
	}

	// Send the agent's output back to the user.
	if err := g.bot.SendMessage(ctx, chatID, result.Output); err != nil {
		log.Printf("gateway: SendMessage (chat=%d): %v", chatID, err)
	}

	// Fire summary generation in background after responding.
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

	// Log activity in background.
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

// renderSystemPrompt builds a rendered system prompt for the user. If profile
// fetching fails or profiles is nil, it falls back to a default rendering with
// no profile data.
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
			// No profile row yet — this is a new user.
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

// userMutex returns the per-user mutex for telegramID, creating it if needed.
func (g *Gateway) userMutex(telegramID int64) *sync.Mutex {
	v, _ := g.mu.LoadOrStore(telegramID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// sendError sends the standard error message to chatID, logging any failure.
func (g *Gateway) sendError(ctx context.Context, chatID int64) {
	if err := g.bot.SendMessage(ctx, chatID, "Sorry, something went wrong. Please try again."); err != nil {
		log.Printf("gateway: sendError (chat=%d): %v", chatID, err)
	}
}
