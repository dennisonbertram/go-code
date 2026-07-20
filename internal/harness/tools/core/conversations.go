package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

const (
	defaultListConversationsLimit   = 20
	maxListConversationsLimit       = 100
	defaultSearchConversationsLimit = 10
	maxSearchConversationsLimit     = 50
)

// listConversationsArgs holds the arguments for the list_conversations tool.
type listConversationsArgs struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// ListConversationsTool returns a core tool that lists recent conversations by metadata.
func ListConversationsTool(store tools.ConversationReader) tools.Tool {
	def := tools.Definition{
		Name:         "list_conversations",
		Description:  descriptions.Load("list_conversations"),
		Action:       tools.ActionList,
		Mutating:     false,
		ParallelSafe: true,
		Tier:         tools.TierCore,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of conversations to return (default 20, max 100).",
					"minimum":     1,
					"maximum":     maxListConversationsLimit,
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Number of conversations to skip for pagination (default 0).",
					"minimum":     0,
				},
			},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		if store == nil {
			return "", fmt.Errorf("conversation store is not configured")
		}

		var args listConversationsArgs
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse list_conversations args: %w", err)
			}
		}

		limit := args.Limit
		if limit <= 0 {
			limit = defaultListConversationsLimit
		}
		if limit > maxListConversationsLimit {
			limit = maxListConversationsLimit
		}

		offset := args.Offset
		if offset < 0 {
			offset = 0
		}

		convs, err := store.ListConversations(ctx, limit, offset)
		if err != nil {
			return "", fmt.Errorf("list conversations: %w", err)
		}

		return tools.MarshalToolResult(map[string]any{
			"conversations": convs,
			"count":         len(convs),
			"limit":         limit,
			"offset":        offset,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// searchConversationsArgs holds the arguments for the search_conversations tool.
type searchConversationsArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// SearchConversationsTool returns a core tool that searches conversation history by full-text query.
func SearchConversationsTool(store tools.ConversationReader) tools.Tool {
	def := tools.Definition{
		Name:         "search_conversations",
		Description:  descriptions.Load("search_conversations"),
		Action:       tools.ActionRead,
		Mutating:     false,
		ParallelSafe: true,
		Tier:         tools.TierCore,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Full-text search query. Supports FTS5 syntax (phrases, NOT/OR/AND).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default 10, max 50).",
					"minimum":     1,
					"maximum":     maxSearchConversationsLimit,
				},
			},
			"required": []string{"query"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		if store == nil {
			return "", fmt.Errorf("conversation store is not configured")
		}

		var args searchConversationsArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse search_conversations args: %w", err)
		}

		query := strings.TrimSpace(args.Query)
		if query == "" {
			return "", fmt.Errorf("query is required")
		}

		limit := args.Limit
		if limit <= 0 {
			limit = defaultSearchConversationsLimit
		}
		if limit > maxSearchConversationsLimit {
			limit = maxSearchConversationsLimit
		}

		results, err := store.SearchConversations(ctx, query, limit)
		if err != nil {
			return "", fmt.Errorf("search conversations: %w", err)
		}

		return tools.MarshalToolResult(map[string]any{
			"results": results,
			"count":   len(results),
			"query":   query,
			"limit":   limit,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}
