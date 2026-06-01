package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	telegramAPIBase = "https://api.telegram.org/bot"
	maxTextRunes    = 4096
	defaultTimeout  = 10 * time.Second
)

// Telegram sends messages via the Telegram Bot API using a shared HTTP client.
type Telegram struct {
	client *http.Client
}

// NewTelegram creates a Telegram sender with a default 10-second HTTP client.
func NewTelegram() *Telegram {
	return &Telegram{client: &http.Client{Timeout: defaultTimeout}}
}

// NewTelegramWithClient creates a Telegram sender using the provided HTTP client.
// Intended for testing — pass a custom *http.Client to intercept or mock calls.
func NewTelegramWithClient(client *http.Client) *Telegram {
	return &Telegram{client: client}
}

// Send delivers text to chatID using the given bot token. If silent is true,
// the message is delivered without sound. Text is truncated to 4096 runes with
// "..." if it exceeds the limit. An empty text returns an error without an API call.
func (t *Telegram) Send(ctx context.Context, token, chatID, text string, silent bool) error {
	if text == "" {
		return fmt.Errorf("telegram: message text must not be empty")
	}

	text = truncateRunes(text, maxTextRunes)

	payload := struct {
		ChatID              string `json:"chat_id"`
		Text                string `json:"text"`
		ParseMode           string `json:"parse_mode"`
		DisableNotification bool   `json:"disable_notification"`
	}{
		ChatID:              chatID,
		Text:                text,
		ParseMode:           "HTML",
		DisableNotification: silent,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: marshalling request: %w", err)
	}

	url := telegramAPIBase + token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: sending message: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Description string `json:"description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		if apiErr.Description != "" {
			return fmt.Errorf("telegram: API error %d: %s", resp.StatusCode, apiErr.Description)
		}
		return fmt.Errorf("telegram: unexpected status %d", resp.StatusCode)
	}

	return nil
}

// truncateRunes truncates s to at most max runes. If truncation occurs, the
// last three runes are replaced with "..." to signal the cut.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-3]) + "..."
}
