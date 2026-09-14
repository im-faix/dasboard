FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /sentinel ./cmd/sentinel-api
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /sentinel /sentinel
COPY --from=build /src/internal/api/router/dashboard-reference.html /app/dashboard.html
USER nonroot:nonroot
ENV STATIC_FILE=/app/dashboard.html
EXPOSE 8080
ENTRYPOINT ["/sentinel"]
