package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func captureHandler(name *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*name = TokenNameFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func TestBearerTokenMiddleware(t *testing.T) {
	tokens := []TokenEntry{
		{Name: "alice", Value: "secret-alice"},
		{Name: "bob", Value: "secret-bob"},
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
			BearerTokenMiddleware(tokens, nil, nil, nil)(captureHandler(&gotName)).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if gotName != tc.wantName {
				t.Errorf("token name in context = %q, want %q", gotName, tc.wantName)
			}
		})
	}
}

// TestBearerTokenMiddleware_NoEarlyExit verifies the constant-time property:
// all tokens are always iterated, so a match at the end of the list still succeeds.
func TestBearerTokenMiddleware_NoEarlyExit(t *testing.T) {
	tokens := []TokenEntry{
		{Name: "a", Value: "aaa"},
		{Name: "b", Value: "bbb"},
		{Name: "c", Value: "ccc"},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer ccc")
	rec := httptest.NewRecorder()

	var gotName string
	BearerTokenMiddleware(tokens, nil, nil, nil)(captureHandler(&gotName)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotName != "c" {
		t.Errorf("token name = %q, want %q", gotName, "c")
	}
}

func TestBearerTokenMiddleware_EmptyList(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()

	BearerTokenMiddleware(nil, nil, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("empty token list: status = %d, want 401", rec.Code)
	}
}
