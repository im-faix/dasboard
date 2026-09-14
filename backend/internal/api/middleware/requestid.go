package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-ID"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		id := uuid.NewString()

		w.Header().Set(RequestIDHeader, id)

		ctx := r.Context()

		ctx = context.WithValue(ctx, RequestIDHeader, id)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
