package mcp

import (
	"context"
	"fmt"
	"slices"

	"github.com/kleist-dev/logmcp/internal/auth"
	"github.com/kleist-dev/logmcp/internal/notify"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerNotifyTools adds the notify_send and notify_bots MCP tools.
// Called from registerTools() only when s.notifyService is non-nil.
func (s *Server) registerNotifyTools() {
	// --- notify_send ---
	if s.toolEnabled("notify_send") {
		s.trackTool("notify_send")
		notifySendTool := mcp.NewTool("notify_send",
			mcp.WithDescription("Send a Telegram message via a configured bot. "+
				"The bot must be listed in telegram.yaml for the calling user. "+
				"Omit 'bot' when you have exactly one configured bot."),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("Message text (HTML formatting supported). Truncated to 4096 characters if longer."),
			),
			mcp.WithString("bot",
				mcp.Description("Bot name to send through. Required when the user has more than one configured bot."),
			),
			mcp.WithBoolean("silent",
				mcp.Description("If true, deliver the message without sound notification."),
				mcp.DefaultBool(false),
			),
		)
		s.mcpSrv.AddTool(notifySendTool, withScope("logmcp:read", s.handleNotifySend))
	}

	// --- notify_bots ---
	if s.toolEnabled("notify_bots") {
		s.trackTool("notify_bots")
		notifyBotsTool := mcp.NewTool("notify_bots",
			mcp.WithDescription("List Telegram bots available to the calling user. "+
				"Admins (logmcp:admin) see all bots with their user mappings."),
		)
		s.mcpSrv.AddTool(notifyBotsTool, withScope("logmcp:read", s.handleNotifyBots))
	}
}

// handleNotifySend implements the notify_send tool.
func (s *Server) handleNotifySend(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.RLock()
	svc := s.notifyService
	s.mu.RUnlock()

	if svc == nil {
		return mcp.NewToolResultError("Telegram notifications are not configured on this server"), nil
	}

	username := auth.TokenNameFromCtx(ctx)
	text := req.GetString("text", "")
	if text == "" {
		return mcp.NewToolResultError("parameter 'text' is required"), nil
	}
	silent := req.GetBool("silent", false)
	botName := req.GetString("bot", "")

	if botName == "" {
		userBots := svc.BotsForUser(username)
		switch len(userBots) {
		case 0:
			return mcp.NewToolResultError(fmt.Sprintf(
				"no Telegram bots are configured for user %q", username)), nil
		case 1:
			botName = userBots[0].Name
		default:
			names := make([]string, len(userBots))
			for i, b := range userBots {
				names[i] = b.Name
			}
			return mcp.NewToolResultError(fmt.Sprintf(
				"multiple bots configured for user %q — specify 'bot' parameter (available: %v)", username, names)), nil
		}
	}

	if err := svc.Send(ctx, username, botName, text, silent); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := struct {
		Sent bool   `json:"sent"`
		Bot  string `json:"bot"`
	}{
		Sent: true,
		Bot:  botName,
	}
	return marshalResult(result)
}

// botEntry is the response item for notify_bots.
// Users holds the user list and is only populated for admin callers.
type botEntry struct {
	Bot   string   `json:"bot"`
	Users []string `json:"users,omitempty"`
}

// handleNotifyBots implements the notify_bots tool.
func (s *Server) handleNotifyBots(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.RLock()
	svc := s.notifyService
	s.mu.RUnlock()

	if svc == nil {
		return mcp.NewToolResultError("Telegram notifications are not configured on this server"), nil
	}

	username := auth.TokenNameFromCtx(ctx)
	isAdmin := hasScope(ctx, "logmcp:admin")

	var bots []notify.BotConfig
	if isAdmin {
		bots = svc.AllBots()
	} else {
		bots = svc.BotsForUser(username)
	}

	entries := make([]botEntry, 0, len(bots))
	for _, bot := range bots {
		e := botEntry{Bot: bot.Name}
		if isAdmin {
			names := make([]string, len(bot.Users))
			for i, u := range bot.Users {
				names[i] = u.Name
			}
			e.Users = names
		}
		entries = append(entries, e)
	}

	return marshalResult(entries)
}

// hasScope reports whether ctx carries the given scope.
func hasScope(ctx context.Context, scope string) bool {
	return slices.Contains(auth.TokenScopesFromCtx(ctx), scope)
}
