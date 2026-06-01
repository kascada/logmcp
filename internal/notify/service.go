package notify

import (
	"context"
	"fmt"
	"slices"
)

// Service resolves bot/user lookups and dispatches Telegram messages.
type Service struct {
	cfg    *TelegramConfig
	sender *Telegram // shared http.Client for all sends
}

// NewService creates a Service from cfg.
func NewService(cfg *TelegramConfig) *Service {
	return &Service{
		cfg:    cfg,
		sender: NewTelegram(),
	}
}

// BotsForUser returns bots where username appears in the users list.
func (s *Service) BotsForUser(username string) []BotConfig {
	var result []BotConfig
	for _, bot := range s.cfg.Bots {
		if slices.ContainsFunc(bot.Users, func(u UserConfig) bool { return u.Name == username }) {
			result = append(result, bot)
		}
	}
	return result
}

// AllBots returns all configured bots.
func (s *Service) AllBots() []BotConfig {
	out := make([]BotConfig, len(s.cfg.Bots))
	copy(out, s.cfg.Bots)
	return out
}

// Send delivers text via the named bot on behalf of username.
// Both bot name and username must be present in the config; otherwise an error
// is returned without making any API call.
func (s *Service) Send(ctx context.Context, username, botName, text string, silent bool) error {
	for _, bot := range s.cfg.Bots {
		if bot.Name != botName {
			continue
		}
		for _, u := range bot.Users {
			if u.Name == username {
				return s.sender.Send(ctx, bot.Token, u.ChatID, text, silent)
			}
		}
		return fmt.Errorf("bot %q is not configured for user %q", botName, username)
	}
	return fmt.Errorf("bot %q is not configured", botName)
}
