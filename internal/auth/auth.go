package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/kleist-dev/logmcp/internal/audit"
)

// TokenEntry is a bearer token the middleware will accept.
type TokenEntry struct {
	Name   string
	Value  string
	Scopes []string
}

// tokenNameKey is the context key for the matched token name.
type tokenNameKey struct{}

// TokenNameFromCtx retrieves the matched token name injected by BearerTokenMiddleware.
func TokenNameFromCtx(ctx context.Context) string {
	if name, ok := ctx.Value(tokenNameKey{}).(string); ok {
		return name
	}
	return ""
}

// errorResponse is the JSON body returned on authentication failure.
type errorResponse struct {
	Error string `json:"error"`
}

// BearerTokenMiddleware returns an HTTP middleware that enforces Bearer token
// authentication. getIP extracts the client IP for logging; pass nil to use
// r.RemoteAddr. burst and sustained are optional rate limiters; pass nil to
// disable the respective tier. Both are checked independently before the token
// is inspected — each tier blocks on its own threshold.
func BearerTokenMiddleware(tokens []TokenEntry, getIP func(*http.Request) string, burst, sustained *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, getIP)

			// Burst check — blocks IPs that have exceeded the short-window threshold.
			if burst != nil && burst.IsBlocked(ip) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(errorResponse{Error: "too many failed auth attempts"})
				return
			}
			// Sustained check — blocks IPs that have exceeded the long-window threshold.
			if sustained != nil && sustained.IsBlocked(ip) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(errorResponse{Error: "too many failed auth attempts"})
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				recordFailure(ip, "missing_header", burst, sustained)
				writeError(w, http.StatusUnauthorized, "missing Authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				recordFailure(ip, "bad_scheme", burst, sustained)
				writeError(w, http.StatusUnauthorized, "Authorization header must use Bearer scheme")
				return
			}

			provided := []byte(parts[1])

			// Iterate all tokens without early exit to avoid timing side-channels.
			matchedName := ""
			found := 0
			for i := range tokens {
				if subtle.ConstantTimeCompare(provided, []byte(tokens[i].Value)) == 1 {
					matchedName = tokens[i].Name
					found = 1
				}
			}
			if found == 0 {
				recordFailure(ip, "invalid_token", burst, sustained)
				writeError(w, http.StatusUnauthorized, "invalid bearer token")
				return
			}

			ctx := context.WithValue(r.Context(), tokenNameKey{}, matchedName)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// recordFailure logs an auth failure to syslog and records it in both rate limiters.
func recordFailure(ip, reason string, burst, sustained *RateLimiter) {
	_ = audit.LogAuthFailed(ip, reason)
	if burst != nil {
		burst.Record(ip)
	}
	if sustained != nil {
		sustained.Record(ip)
	}
}

// clientIP returns the request's client IP using getIP, falling back to RemoteAddr.
func clientIP(r *http.Request, getIP func(*http.Request) string) string {
	if getIP != nil {
		return getIP(r)
	}
	// Trim port from RemoteAddr.
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="logmcp"`)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}
