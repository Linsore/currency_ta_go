// internal/httpx/cors.go
package httpx

import (
	"net/http"
	"strings"
)

// NewCORS returns middleware that allows specific origins and common headers.
func NewCORS(allowOriginsCSV string) func(http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	for _, o := range strings.Split(allowOriginsCSV, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			allowed[o] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowed["*"]; ok || (origin != "" && originAllowed(origin, allowed)) {
					w.Header().Set("Access-Control-Allow-Origin", origin) // echo origin (or "*")
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "false")
					w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers",
						"Content-Type, Authorization, Idempotency-Key, X-Requested-With")
					w.Header().Set("Access-Control-Max-Age", "600")
				}
			}
			// Handle preflight
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func originAllowed(o string, set map[string]struct{}) bool {
	_, ok := set[o]
	return ok
}
