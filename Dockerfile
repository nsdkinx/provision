# Builder
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o provision-server ./cmd/server

# Runner
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/provision-server .
ENV DATABASE_PATH=/var/lib/provision/provision.db
ENV DATA_DIR=/var/lib/provision/storage
ENV PORT=8000
EXPOSE 8000
CMD ["./provision-server"]
