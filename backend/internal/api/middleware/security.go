package middleware

import "net/http"

func Security(next http.Handler) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		w.Header().Set("X-Frame-Options", "DENY")

		w.Header().Set("X-Content-Type-Options", "nosniff")

		w.Header().Set("Referrer-Policy", "no-referrer")

		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")

		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

		next.ServeHTTP(w, r)

	})
}
