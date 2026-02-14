# API Gateway

[![CI](https://img.shields.io/github/actions/workflow/status/bimakw/api-gateway/ci.yml?branch=main)](https://github.com/bimakw/api-gateway/actions)

Reverse proxy gateway with rate limiting (token bucket + Redis), circuit breakers, retry with exponential backoff, API key auth, and Prometheus metrics. Written in Go.

## Running

```bash
cp .env.example .env
make run            # or: make docker-up
```

Requires Redis 7+ for rate limiting and API key storage.

### Key Config

| Variable | Default | Notes |
|----------|---------|-------|
| `PORT` | `8081` | Gateway port |
| `RATE_LIMIT_RPM` | `60` | Requests/minute per client |
| `CB_MAX_FAILURES` | `5` | Failures before circuit opens |
| `CB_RESET_TIMEOUT_SECONDS` | `30` | Open → half-open delay |
| `RETRY_MAX_RETRIES` | `3` | Max retry attempts |
| `ADMIN_AUTH_ENABLED` | `true` | Basic auth on `/admin/*` |

See `.env.example` for the full list.

## Endpoints

**Management**: `/health`, `/info`, `/metrics`, `/services/health`

**Admin** (Basic Auth): `/admin/apikeys` (CRUD), `/admin/circuit-breakers` (stats + reset)

**Proxy**: everything else routes to backends by path prefix (`/api/auth/*` → auth-service, `/api/users/*` → user-service).

## Testing

```bash
go test ./...
```

## License

MIT
