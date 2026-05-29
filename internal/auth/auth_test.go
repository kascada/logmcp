package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func captureHandler(name *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*name = TokenNameFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddleware_StaticResolver(t *testing.T) {
	tokens := []TokenEntry{
		{Name: "alice", Value: "secret-alice", Scopes: []string{"logmcp:read"}},
		{Name: "bob", Value: "secret-bob", Scopes: []string{"logmcp:read"}},
	}

	tests := []struct {
		name       string
		header     string
		wantStatus int
		wantName   string
	}{
		{"missing header", "", http.StatusUnauthorized, ""},
		{"wrong scheme", "Basic dXNlcjpwYXNz", http.StatusUnauthorized, ""},
		{"invalid token", "Bearer wrong", http.StatusUnauthorized, ""},
		{"valid first token", "Bearer secret-alice", http.StatusOK, "alice"},
		{"valid second token", "Bearer secret-bob", http.StatusOK, "bob"},
		{"scheme case insensitive", "bearer secret-alice", http.StatusOK, "alice"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotName string
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			Middleware(StaticResolver(tokens), nil, nil, nil)(captureHandler(&gotName)).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if gotName != tc.wantName {
				t.Errorf("token name in context = %q, want %q", gotName, tc.wantName)
			}
		})
	}
}

// TestStaticResolver_NoEarlyExit verifies the constant-time property:
// all tokens are always iterated, so a match at the end of the list still succeeds.
func TestStaticResolver_NoEarlyExit(t *testing.T) {
	tokens := []TokenEntry{
		{Name: "a", Value: "aaa", Scopes: []string{"logmcp:read"}},
		{Name: "b", Value: "bbb", Scopes: []string{"logmcp:read"}},
		{Name: "c", Value: "ccc", Scopes: []string{"logmcp:read"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer ccc")
	rec := httptest.NewRecorder()

	var gotName string
	Middleware(StaticResolver(tokens), nil, nil, nil)(captureHandler(&gotName)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotName != "c" {
		t.Errorf("token name = %q, want %q", gotName, "c")
	}
}

func TestMiddleware_EmptyTokenList(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()

	Middleware(StaticResolver(nil), nil, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("empty token list: status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_ScopesInContext(t *testing.T) {
	tokens := []TokenEntry{
		{Name: "alice", Value: "secret-alice", Scopes: []string{"logmcp:read", "sb:read"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret-alice")
	rec := httptest.NewRecorder()

	var gotScopes []string
	Middleware(StaticResolver(tokens), nil, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotScopes = TokenScopesFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(gotScopes) != 2 || gotScopes[0] != "logmcp:read" || gotScopes[1] != "sb:read" {
		t.Errorf("scopes in context = %v, want [logmcp:read sb:read]", gotScopes)
	}
}

func TestRequireScope(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Run("scope present", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := req.Context()
		ctx = withScopes(ctx, []string{"logmcp:read", "sb:read"})
		rec := httptest.NewRecorder()
		RequireScope("logmcp:read")(ok).ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("scope missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := withScopes(req.Context(), []string{"sb:read"})
		rec := httptest.NewRecorder()
		RequireScope("logmcp:read")(ok).ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("no scopes in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		RequireScope("logmcp:read")(ok).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})
}

func withScopes(ctx context.Context, scopes []string) context.Context {
	return context.WithValue(ctx, tokenScopesKey{}, scopes)
}

func blockedLimiter() *RateLimiter {
	rl := NewRateLimiter(1, time.Minute)
	rl.Record("192.0.2.1")
	return rl
}

func clearLimiter() *RateLimiter {
	return NewRateLimiter(5, time.Minute)
}

func TestMiddleware_TwoTierRateLimit(t *testing.T) {
	validTokens := []TokenEntry{{Name: "alice", Value: "secret-alice", Scopes: []string{"logmcp:read"}}}

	tests := []struct {
		name       string
		burst      *RateLimiter
		sustained  *RateLimiter
		authHeader string
		wantStatus int
	}{
		{
			name:       "burst blocks, sustained nil",
			burst:      blockedLimiter(),
			sustained:  nil,
			authHeader: "Bearer secret-alice",
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "sustained blocks, burst nil",
			burst:      nil,
			sustained:  blockedLimiter(),
			authHeader: "Bearer secret-alice",
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "burst blocks, sustained clear",
			burst:      blockedLimiter(),
			sustained:  clearLimiter(),
			authHeader: "Bearer secret-alice",
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "sustained blocks, burst clear",
			burst:      clearLimiter(),
			sustained:  blockedLimiter(),
			authHeader: "Bearer secret-alice",
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "both block",
			burst:      blockedLimiter(),
			sustained:  blockedLimiter(),
			authHeader: "Bearer secret-alice",
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "neither blocks, valid token",
			burst:      clearLimiter(),
			sustained:  clearLimiter(),
			authHeader: "Bearer secret-alice",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", tc.authHeader)
			rec := httptest.NewRecorder()

			Middleware(StaticResolver(validTokens), nil, tc.burst, tc.sustained)(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}),
			).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}

func TestMiddleware_RecordFailureWritesToBothLimiters(t *testing.T) {
	burst := NewRateLimiter(2, time.Minute)
	sustained := NewRateLimiter(2, time.Minute)
	tokens := []TokenEntry{{Name: "alice", Value: "secret-alice", Scopes: []string{"logmcp:read"}}}

	sendInvalid := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer invalid_token")
		rec := httptest.NewRecorder()
		Middleware(StaticResolver(tokens), nil, burst, sustained)(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		).ServeHTTP(rec, req)
		return rec.Code
	}

	if code := sendInvalid(); code != http.StatusUnauthorized {
		t.Fatalf("first attempt: status = %d, want 401", code)
	}

	ip := "192.0.2.1"
	if burst.IsBlocked(ip) {
		t.Error("burst should not be blocked after one failure with threshold 2")
	}
	if sustained.IsBlocked(ip) {
		t.Error("sustained should not be blocked after one failure with threshold 2")
	}

	sendInvalid()

	if !burst.IsBlocked(ip) {
		t.Error("burst should be blocked after two failures with threshold 2")
	}
	if !sustained.IsBlocked(ip) {
		t.Error("sustained should be blocked after two failures with threshold 2")
	}
}
