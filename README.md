# chatbot-api

Gin + PostgreSQL API for a company chatbot. Chat completions go through an OpenAI-compatible gateway (default: local `cursor-api` at `http://127.0.0.1:8130/v1`).

## Local setup

```powershell
docker compose up -d
copy .env.example .env
# Optional fallback: CHAT_API_KEY. Prefer Admin → Settings in the WebUI.
go mod tidy
go run ./cmd/server
```

- API: `http://localhost:8134`
- Postgres: `localhost:5442` (user/pass/db `chatbot`)
- Default login: `armin` / `dopadopa123` (admin)

Chat replies use the API key from **Admin → Settings**. `.env` `CHAT_API_KEY` is only a fallback if Settings is empty. Without a key, the API still stores messages and returns a stub assistant reply.

## Health

`GET /health`
