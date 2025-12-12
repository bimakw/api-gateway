# API Gateway

A lightweight, high-performance API Gateway built with Go, featuring rate limiting, API key management, and reverse proxy capabilities.

## Features

- **Rate Limiting**: Token bucket algorithm with Redis backend
- **API Key Management**: Create, list, revoke, and delete API keys
- **Reverse Proxy**: Route requests to multiple backend services
- **Middleware Chain**: Logging, CORS, authentication, rate limiting
- **Graceful Shutdown**: Clean shutdown with connection draining
- **Health Checks**: Monitor gateway and backend service health

## Tech Stack

- **Language**: Go 1.22+
- **Cache/Storage**: Redis 7+
- **Container**: Docker & Docker Compose

## API Endpoints

### Gateway Management

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check |
| GET | `/info` | Gateway info and registered services |
| POST | `/admin/apikeys` | Create new API key |
| GET | `/admin/apikeys` | List all API keys |
| POST | `/admin/apikeys/{id}/revoke` | Revoke an API key |
| DELETE | `/admin/apikeys/{id}` | Delete an API key |

### Proxy Routes

All other requests are proxied to backend services based on path prefix:

| Path Prefix | Target Service |
|-------------|----------------|
| `/api/auth/*` | Auth Service |
| `/api/users/*` | User Service |

## Getting Started

### Prerequisites

- Go 1.22+
- Docker & Docker Compose
- Redis 7+

### Installation

1. Clone the repository:
```bash
git clone https://github.com/bimakw/api-gateway.git
cd api-gateway
```

2. Copy environment file:
```bash
cp .env.example .env
```

3. Install dependencies:
```bash
go mod download
```

4. Run with Docker:
```bash
make docker-up
```

Or run locally:
```bash
# Start Redis first
docker run -d -p 6379:6379 redis:7-alpine

# Run the gateway
make run
```

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `HOST` | Server host | `0.0.0.0` |
| `PORT` | Server port | `8081` |
| `REDIS_HOST` | Redis host | `localhost` |
| `REDIS_PORT` | Redis port | `6379` |
| `REDIS_PASSWORD` | Redis password | `` |
| `REDIS_DB` | Redis database | `0` |
| `RATE_LIMIT_RPM` | Requests per minute | `60` |
| `RATE_LIMIT_BURST` | Burst size | `10` |
| `AUTH_SERVICE_URL` | Auth service URL | `http://localhost:8080` |
| `USER_SERVICE_URL` | User service URL | `http://localhost:8082` |

## Usage Examples

### Create API Key

```bash
curl -X POST http://localhost:8081/admin/apikeys \
  -H "Content-Type: application/json" \
  -d '{"name": "my-app", "rate_limit": 100}'
```

Response:
```json
{
  "status": "success",
  "message": "API key created. Save the raw_key - it won't be shown again!",
  "data": {
    "api_key": {
      "id": "abc123",
      "name": "my-app",
      "rate_limit": 100,
      "active": true
    },
    "raw_key": "your-secret-api-key-here"
  }
}
```

### Use API Key

```bash
# Via X-API-Key header
curl -X GET http://localhost:8081/api/auth/me \
  -H "X-API-Key: your-secret-api-key-here"

# Or via Authorization header
curl -X GET http://localhost:8081/api/auth/me \
  -H "Authorization: Bearer your-secret-api-key-here"
```

### Check Rate Limit Status

Response headers include:
- `X-RateLimit-Remaining`: Remaining requests in current window
- `X-RateLimit-Reset`: Unix timestamp when the window resets

### Health Check

```bash
curl http://localhost:8081/health
```

## Rate Limiting

The gateway implements a token bucket algorithm:

- Each client (identified by IP or API key) has a bucket of tokens
- Tokens refill at a rate of `RATE_LIMIT_RPM` per minute
- Maximum bucket size is `RATE_LIMIT_BURST`
- Each request consumes one token
- When bucket is empty, requests receive `429 Too Many Requests`

API keys can have custom rate limits that override the default.

## Project Structure

```
api-gateway/
├── cmd/
│   └── gateway/
│       └── main.go           # Entry point
├── config/
│   └── config.go             # Configuration
├── internal/
│   ├── apikey/
│   │   └── apikey.go         # API key management
│   ├── handler/
│   │   └── handler.go        # HTTP handlers
│   ├── middleware/
│   │   └── middleware.go     # Middleware chain
│   ├── proxy/
│   │   └── proxy.go          # Reverse proxy
│   └── ratelimit/
│       └── ratelimit.go      # Rate limiter
├── .env.example
├── .gitignore
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## Development

```bash
# Run in development mode
make run

# Run tests
make test

# Format code
make fmt

# Build binary
make build
```

## License

MIT License
