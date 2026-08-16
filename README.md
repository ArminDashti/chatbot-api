# chatbot-api

Gin + PostgreSQL API for a company chatbot. Chat completions go through an OpenAI-compatible gateway (default: local `cursor-api` at `http://127.0.0.1:8130/v1`).

## Local setup

```powershell
docker compose up -d
copy .env.example .env
# Set CHAT_API_KEY to a cursor-api gateway key (ck_...)
go mod tidy
go run ./cmd/server
```

- API: `http://localhost:8134`
- Postgres: `localhost:5442` (user/pass/db `chatbot`)
- Default login: `armin` / `dopadopa123` (admin)

Chat replies need a running `cursor-api` and `CHAT_API_KEY` in `.env`. Without a key, the API still stores messages and returns a stub assistant reply.

## Health

`GET /health`
