package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

const testToken = "testtoken"
const testChatID = "-100123456"

// newTestTelegram creates a Telegram sender wired to the given test server.
func newTestTelegram(srv *httptest.Server) *Telegram {
	client := &http.Client{
		Transport: &hostRewriter{target: srv.URL, inner: http.DefaultTransport},
	}
	return NewTelegramWithClient(client)
}

// hostRewriter is an http.RoundTripper that rewrites the request host to a fixed target.
type hostRewriter struct {
	target string
	inner  http.RoundTripper
}

func (h *hostRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(h.target, "http://")
	return h.inner.RoundTrip(clone)
}

func TestTelegram_Send_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	tg := newTestTelegram(srv)
	if err := tg.Send(context.Background(), testToken, testChatID, "hello world", false); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestTelegram_Send_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ok":false,"description":"chat not found"}`)
	}))
	defer srv.Close()

	tg := newTestTelegram(srv)
	err := tg.Send(context.Background(), testToken, testChatID, "hello", false)
	if err == nil {
		t.Fatal("expected error for 4xx response, got nil")
	}
}

func TestTelegram_Send_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	tg := newTestTelegram(srv)
	err := tg.Send(context.Background(), testToken, testChatID, "test", false)
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

func TestTelegram_Send_EmptyText(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tg := newTestTelegram(srv)
	err := tg.Send(context.Background(), testToken, testChatID, "", false)
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
	if called {
		t.Fatal("expected no API call for empty text, but server was called")
	}
}

func TestTelegram_Send_RuneTruncation(t *testing.T) {
	longText := strings.Repeat("a", maxTextRunes+1)

	var capturedText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			capturedText = payload.Text
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	tg := newTestTelegram(srv)
	if err := tg.Send(context.Background(), testToken, testChatID, longText, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n := utf8.RuneCountInString(capturedText); n != maxTextRunes {
		t.Fatalf("expected %d runes in sent text, got %d", maxTextRunes, n)
	}
	if !strings.HasSuffix(capturedText, "...") {
		t.Fatalf("expected truncated text to end with '...', got suffix: %q", capturedText[len(capturedText)-6:])
	}
}

func TestTruncateRunes_NoTruncation(t *testing.T) {
	s := strings.Repeat("x", 100)
	if got := truncateRunes(s, maxTextRunes); got != s {
		t.Fatal("expected unchanged string for short input")
	}
}

func TestTruncateRunes_ExactBoundary(t *testing.T) {
	s := strings.Repeat("x", maxTextRunes)
	if got := truncateRunes(s, maxTextRunes); got != s {
		t.Fatal("expected unchanged string at exact boundary")
	}
}

func TestTruncateRunes_MultibyteRunes(t *testing.T) {
	s := strings.Repeat("あ", maxTextRunes+5)
	got := truncateRunes(s, maxTextRunes)
	if n := utf8.RuneCountInString(got); n != maxTextRunes {
		t.Fatalf("expected %d runes after truncation, got %d", maxTextRunes, n)
	}
}
