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

// tokenNameKey is the context key for the caller's token name.
type tokenNameKey struct{}

// tokenValueKey is the context key for the raw Bearer token value.
type tokenValueKey struct{}

// tokenScopesKey is the context key for the caller's scopes.
type tokenScopesKey struct{}

// TokenNameFromCtx retrieves the caller name stored by Middleware.
func TokenNameFromCtx(ctx context.Context) string {
	if name, ok := ctx.Value(tokenNameKey{}).(string); ok {
		return name
	}
	return ""
}

// TokenValueFromCtx retrieves the raw Bearer token stored by Middleware.
func TokenValueFromCtx(ctx context.Context) string {
	if val, ok := ctx.Value(tokenValueKey{}).(string); ok {
		return val
	}
	return ""
}

// TokenScopesFromCtx retrieves the scopes stored by Middleware.
func TokenScopesFromCtx(ctx context.Context) []string {
	if scopes, ok := ctx.Value(tokenScopesKey{}).([]string); ok {
		return scopes
	}
	return nil
}

// InjectStdioIdentity stores a synthetic caller identity into the context for
// stdio transport, where there is no HTTP middleware to inject auth values.
// name is the display name shown in audit entries; scopes controls tool access.
func InjectStdioIdentity(ctx context.Context, name string, scopes []string) context.Context {
	ctx = context.WithValue(ctx, tokenNameKey{}, name)
	ctx = context.WithValue(ctx, tokenValueKey{}, "")
	ctx = context.WithValue(ctx, tokenScopesKey{}, scopes)
	return ctx
}

// errorResponse is the JSON body returned on authentication failure.
type errorResponse struct {
	Error string `json:"error"`
}

// VerifyFunc resolves a bearer token to the caller's identity and scopes.
// ok=false means the token is not recognized. err signals an infrastructure failure.
type VerifyFunc func(token string) (name string, scopes []string, ok bool, err error)

// StaticResolver returns a VerifyFunc backed by a fixed token list.
// All entries are always checked to avoid timing side-channels.
func StaticResolver(tokens []TokenEntry) VerifyFunc {
	return func(token string) (string, []string, bool, error) {
		provided := []byte(token)
		name, scopes := "", []string(nil)
		found := 0
		for i := range tokens {
			if subtle.ConstantTimeCompare(provided, []byte(tokens[i].Value)) == 1 {
				name = tokens[i].Name
				scopes = tokens[i].Scopes
				found = 1
			}
		}
		if found == 0 {
			return "", nil, false, nil
		}
		return name, scopes, true, nil
	}
}

// Middleware extracts the Bearer token, resolves it via resolve, and stores
// the caller name and scopes in the request context. No scope checking is done here.
// Returns 401 if the token is missing or not recognized, 500 on infrastructure error.
func Middleware(resolve VerifyFunc, getIP func(*http.Request) string, burst, sustained *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, getIP)

			if burst != nil && burst.IsBlocked(ip) {
				writeTooManyRequests(w)
				return
			}
			if sustained != nil && sustained.IsBlocked(ip) {
				writeTooManyRequests(w)
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

			token := parts[1]
			name, scopes, ok, err := resolve(token)
			if err != nil {
				_ = audit.LogAuthFailed(ip, "authenticator_error")
				writeError(w, http.StatusInternalServerError, "authentication service unavailable")
				return
			}
			if !ok {
				recordFailure(ip, "invalid_token", burst, sustained)
				writeError(w, http.StatusUnauthorized, "invalid bearer token")
				return
			}

			ctx := context.WithValue(r.Context(), tokenNameKey{}, name)
			ctx = context.WithValue(ctx, tokenValueKey{}, token)
			ctx = context.WithValue(ctx, tokenScopesKey{}, scopes)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireScope returns HTTP middleware that rejects requests whose resolved
// scopes do not include scope. Must be applied after Middleware.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, s := range TokenScopesFromCtx(r.Context()) {
				if s == scope {
					next.ServeHTTP(w, r)
					return
				}
			}
			writeError(w, http.StatusForbidden, "missing required scope: "+scope)
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
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

func writeTooManyRequests(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: "too many failed auth attempts"})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="logmcp"`)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}
