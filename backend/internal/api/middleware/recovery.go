package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/im-faix/sentinel/backend/internal/api/response"
)

func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {

	return func(next http.Handler) http.Handler {

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			defer func() {

				if err := recover(); err != nil {

					logger.Error(
						"Panic recovered",
						slog.Any("panic", err),
						slog.String("stack", string(debug.Stack())),
					)

					response.Error(
						w,
						http.StatusInternalServerError,
						"INTERNAL_SERVER_ERROR",
						"Unexpected server error",
					)

				}

			}()

			next.ServeHTTP(w, r)

		})

	}

}
