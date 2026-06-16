Search for and activate additional tools by keyword. Use this when you need a capability not available in your current tools. Activated tools become available for the remainder of this conversation.

## WHEN TO USE
- You need a tool that is not in your current tool list and you suspect it exists but is hidden (deferred).
- You want to discover what additional capabilities are available for a domain (code search, model listing, MCP access, etc.).
- Before resorting to bash for a task that a dedicated tool might handle better.

## WHEN NOT TO USE
- Do NOT use find_tool when you already have the tool you need in your active tool set. Check your available tools first.
- Do NOT use find_tool just to browse — each activation consumes context budget. Only activate tools you genuinely need for the current task.
- Do NOT activate every available tool at once. Activate tools incrementally as you discover you need them.

## Common mistakes
- WRONG: calling find_tool multiple times before checking whether a previously activated tool already covers the need. Review your current tool list first.
- WRONG: activating tools speculatively "just in case." Each activated tool adds to the context sent to the model on every turn.

## Behavioral rules
- Use 'select:<tool_name>' to activate a specific tool by name after searching.
- Activated tools remain available for the rest of the conversation — you do not need to re-activate them.
- Before using bash for any of these tasks, search here first:
  - Code search across repositories → search "sourcegraph"
  - Listing available LLM models → search "models"
  - MCP server resources → search "mcp"
