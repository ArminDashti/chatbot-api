# chatbot-api

Gin + PostgreSQL API for a company chatbot. Chat completions go through an OpenAI-compatible gateway (default: local `cursor-api` at `http://127.0.0.1:8130/v1`).

## Local Docker (API + WebUI + Postgres, hot reload)

From this repo:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\.armin\docker-scripts\run-on-docker-local.ps1
```

- WebUI: `http://localhost:5184`
- API: `http://localhost:8134`
- Postgres: `localhost:5442` (user/pass/db `chatbot`)
- Default login: `armin` / `dopadopa123` (admin)

Save a Go or Vue file and Air / Vite reload the matching container. The WebUI proxies `/api` to the API container. Chat completions still need `cursor-api` on the host at port `8130` (reached as `host.docker.internal` from the API container).

## Local setup (Postgres in Docker, API on the host)

```powershell
docker compose -f docker-compose.local.yml up -d postgres
copy .env.example .env
# Optional fallback: CHAT_API_KEY. Prefer Admin → Settings in the WebUI.
go mod tidy
go run ./cmd/server
```

- API: `http://localhost:8134`
- Postgres: `localhost:5442` (user/pass/db `chatbot`)
- Default login: `armin` / `dopadopa123` (admin)

Chat replies use the API key from **Admin → Settings** (Cursor Cloud `crsr_…` or a gateway `ck_…`). A Settings key always wins over `.env` `CHAT_API_KEY`. Without a key, the API still stores messages and returns a stub assistant reply. `GET /api/v1/chat/ready` reports whether a key and base URL are present. Saved keys are returned only as `**********`.

Support answers use markdown from **Admin → Settings** guide paths (directories or `.md` files). If that list is empty, the API indexes `KNOWLEDGE_DIR` (default `C:\Users\armin\TFS\Source-NewUI\docs`). Indexing runs at startup, on Settings save, and via `POST /api/v1/admin/knowledge/reindex`. Users search chats with `GET /api/v1/conversations?q=` and delete with `DELETE /api/v1/conversations/:id`. Admins manage users with `POST`/`PATCH`/`DELETE /api/v1/admin/users`.

## Health

`GET /health`
