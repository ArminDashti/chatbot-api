# Build from repo root: docker build -f dockerfile -t pc-armin/chatbot:api .
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget
WORKDIR /app
COPY --from=build /out/server /app/server
COPY migrations /app/migrations
ENV ADDR=:8134
ENV MIGRATIONS_DIR=/app/migrations
EXPOSE 8134
CMD ["/app/server"]
