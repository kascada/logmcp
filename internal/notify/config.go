package notify

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// TelegramConfigPath is the hard-coded path for the Telegram configuration file.
const TelegramConfigPath = "/etc/logmcp/telegram.yaml"

// UserConfig maps a token name to a Telegram chat_id.
type UserConfig struct {
	Name   string `yaml:"name"`
	ChatID string `yaml:"chat_id"`
}

// BotConfig describes a single Telegram bot with its token and the users
// (with their chat_ids) allowed to send through it.
type BotConfig struct {
	Name  string       `yaml:"name"`
	Token string       `yaml:"token"`
	Users []UserConfig `yaml:"users"`
}

// TelegramConfig holds all configured bots.
type TelegramConfig struct {
	Bots []BotConfig `yaml:"bots"`
}

// LoadTelegramConfig reads the YAML file at path and returns the parsed config.
// If the file does not exist it returns (nil, false, nil) — the caller should
// treat this as "Telegram disabled", not an error.
func LoadTelegramConfig(path string) (*TelegramConfig, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading telegram config %q: %w", path, err)
	}

	var cfg TelegramConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parsing telegram config %q: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, false, err
	}

	return &cfg, true, nil
}

func (c *TelegramConfig) validate() error {
	seen := make(map[string]struct{}, len(c.Bots))
	for i, bot := range c.Bots {
		if bot.Name == "" {
			return fmt.Errorf("telegram config: bots[%d] has no name", i)
		}
		if bot.Token == "" {
			return fmt.Errorf("telegram config: bot %q has no token", bot.Name)
		}
		if _, dup := seen[bot.Name]; dup {
			return fmt.Errorf("telegram config: duplicate bot name %q", bot.Name)
		}
		seen[bot.Name] = struct{}{}
		for j, u := range bot.Users {
			if u.Name == "" {
				return fmt.Errorf("telegram config: bot %q user[%d] has no name", bot.Name, j)
			}
			if u.ChatID == "" {
				return fmt.Errorf("telegram config: bot %q user %q has no chat_id", bot.Name, u.Name)
			}
		}
	}
	return nil
}
