package channels

import "net/http"

// withSecurityHeaders adds a conservative baseline of HTTP security headers.
//
// Keep this middleware safe for webhook endpoints and the built-in console:
// do not set a CSP here (console uses inline scripts/styles in its HTML).
func withSecurityHeaders(next http.Handler) http.Handler {
	if next == nil {
		next = http.DefaultServeMux
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// Prevent MIME sniffing.
		if h.Get("X-Content-Type-Options") == "" {
			h.Set("X-Content-Type-Options", "nosniff")
		}
		// Disallow embedding in iframes.
		if h.Get("X-Frame-Options") == "" {
			h.Set("X-Frame-Options", "DENY")
		}
		// Avoid leaking internal URLs via Referer.
		if h.Get("Referrer-Policy") == "" {
			h.Set("Referrer-Policy", "no-referrer")
		}
		// Deny powerful features by default.
		if h.Get("Permissions-Policy") == "" {
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		}

		next.ServeHTTP(w, r)
	})
}
