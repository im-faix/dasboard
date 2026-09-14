package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

func Logger(log *slog.Logger) func(http.Handler) http.Handler {

	return func(next http.Handler) http.Handler {

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			start := time.Now()

			next.ServeHTTP(w, r)

			log.Info(
				"request completed",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Duration("duration", time.Since(start)),
			)

		})

	}

}
