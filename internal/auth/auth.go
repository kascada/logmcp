package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
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
// authentication against the provided token list. The name of the matched
// token is injected into the request context for downstream use.
func BearerTokenMiddleware(tokens []TokenEntry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeError(w, http.StatusUnauthorized, "missing Authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
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
				writeError(w, http.StatusUnauthorized, "invalid bearer token")
				return
			}

			ctx := context.WithValue(r.Context(), tokenNameKey{}, matchedName)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="logmcp"`)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}
