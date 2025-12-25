# API Gateway

A lightweight, high-performance API Gateway built with Go, featuring rate limiting, API key management, and reverse proxy capabilities.

## Features

- **Rate Limiting**: Token bucket algorithm with Redis backend
- **API Key Management**: Create, list, revoke, and delete API keys
- **Reverse Proxy**: Route requests to multiple backend services
- **Circuit Breaker**: Prevent cascading failures with automatic recovery
- **Retry Logic**: Automatic retry with exponential backoff for failed requests
- **Admin Authentication**: Basic auth protection for admin endpoints
- **Prometheus Metrics**: Request count, latency percentiles, error rates
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
| GET | `/metrics` | Prometheus metrics endpoint |
| GET | `/services/health` | Backend services health status |
| POST | `/admin/apikeys` | Create new API key |
| GET | `/admin/apikeys` | List all API keys |
| POST | `/admin/apikeys/{id}/revoke` | Revoke an API key |
| DELETE | `/admin/apikeys/{id}` | Delete an API key |
| GET | `/admin/circuit-breakers` | List all circuit breaker stats |
| POST | `/admin/circuit-breakers/{name}/reset` | Reset specific circuit breaker |
| POST | `/admin/circuit-breakers/reset` | Reset all circuit breakers |

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
| `CB_MAX_FAILURES` | Max failures before circuit opens | `5` |
| `CB_RESET_TIMEOUT_SECONDS` | Seconds before circuit tries half-open | `30` |
| `CB_HALF_OPEN_MAX_REQUESTS` | Max requests in half-open state | `3` |
| `CB_SUCCESS_THRESHOLD` | Successes needed to close circuit | `2` |
| `RETRY_MAX_RETRIES` | Maximum retry attempts | `3` |
| `RETRY_INITIAL_DELAY_MS` | Initial delay before first retry (ms) | `100` |
| `RETRY_MAX_DELAY_MS` | Maximum delay between retries (ms) | `5000` |
| `RETRY_MULTIPLIER` | Delay multiplier for exponential backoff | `2.0` |
| `RETRY_JITTER_FACTOR` | Randomness factor to prevent thundering herd | `0.1` |
| `ADMIN_AUTH_ENABLED` | Enable/disable admin authentication | `true` |
| `ADMIN_USERNAME` | Admin username for Basic Auth | `admin` |
| `ADMIN_PASSWORD` | Admin password (required if enabled) | `` |
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

## Circuit Breaker

The gateway implements the circuit breaker pattern to prevent cascading failures:

```
         ┌─────────────────────────────────────────────────────────┐
         │                    Circuit Breaker                      │
         │                                                         │
         │    ┌──────────┐      ┌──────────┐      ┌──────────┐    │
         │    │  CLOSED  │─────►│   OPEN   │─────►│HALF-OPEN │    │
         │    │          │      │          │      │          │    │
         │    └────▲─────┘      └──────────┘      └────┬─────┘    │
         │         │                                    │          │
         │         └────────────────────────────────────┘          │
         │            (success threshold reached)                  │
         └─────────────────────────────────────────────────────────┘
```

**States:**
- **Closed**: Normal operation, requests pass through
- **Open**: Circuit is tripped, requests fail fast with 503
- **Half-Open**: Testing if service recovered, limited requests allowed

**Transitions:**
1. **Closed → Open**: After `CB_MAX_FAILURES` consecutive failures (5xx responses)
2. **Open → Half-Open**: After `CB_RESET_TIMEOUT_SECONDS` have passed
3. **Half-Open → Closed**: After `CB_SUCCESS_THRESHOLD` consecutive successes
4. **Half-Open → Open**: On any failure

### Circuit Breaker Usage

```bash
# Get all circuit breaker stats
curl http://localhost:8081/admin/circuit-breakers
```

Response:
```json
{
  "status": "success",
  "data": [
    {
      "name": "auth-service",
      "state": "closed",
      "failures": 0,
      "consecutive_successes": 5
    },
    {
      "name": "user-service",
      "state": "open",
      "failures": 5,
      "consecutive_successes": 0,
      "last_failure": "2024-01-01T12:00:00Z"
    }
  ]
}
```

```bash
# Reset specific circuit breaker
curl -X POST http://localhost:8081/admin/circuit-breakers/user-service/reset

# Reset all circuit breakers
curl -X POST http://localhost:8081/admin/circuit-breakers/reset
```

## Admin Authentication

Admin endpoints (`/admin/*`) are protected with HTTP Basic Authentication.

### Protected Endpoints

| Endpoint | Description |
|----------|-------------|
| `POST /admin/apikeys` | Create API key |
| `GET /admin/apikeys` | List API keys |
| `POST /admin/apikeys/{id}/revoke` | Revoke API key |
| `DELETE /admin/apikeys/{id}` | Delete API key |
| `GET /admin/circuit-breakers` | List circuit breakers |
| `POST /admin/circuit-breakers/{name}/reset` | Reset circuit breaker |
| `POST /admin/circuit-breakers/reset` | Reset all circuit breakers |

### Configuration

```bash
# Enable admin auth (default: true)
export ADMIN_AUTH_ENABLED=true

# Set admin credentials
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD=your-secure-password
```

**IMPORTANT**: If `ADMIN_AUTH_ENABLED=true` and `ADMIN_PASSWORD` is not set, the gateway will fail to start.

### Usage Examples

```bash
# Access admin endpoint with Basic Auth
curl -u admin:your-password http://localhost:8081/admin/apikeys

# Or using Authorization header
curl -H "Authorization: Basic $(echo -n 'admin:your-password' | base64)" \
  http://localhost:8081/admin/apikeys
```

### Security Features

- **Constant-time comparison**: Prevents timing attacks
- **SHA256 hashing**: Credentials are hashed before comparison
- **Failed attempt logging**: Unauthorized attempts are logged with client IP

### Disabling Admin Auth

To disable admin authentication (not recommended for production):

```bash
export ADMIN_AUTH_ENABLED=false
```

---

## Retry Logic

The gateway automatically retries failed requests to backend services using exponential backoff.

### How It Works

```
Request → Backend Service
           ↓
        Failed? (502, 503, 504)
           ↓
        Retry with delay
           ↓
        delay = initial_delay * (multiplier ^ attempt) + jitter
           ↓
        Max retries exceeded?
           ↓
        Return error response
```

### Retryable Status Codes

The following status codes trigger automatic retry:
- `502 Bad Gateway`
- `503 Service Unavailable`
- `504 Gateway Timeout`

### Exponential Backoff

Delays increase exponentially with each retry:
- Attempt 1: 100ms
- Attempt 2: 200ms
- Attempt 3: 400ms
- (capped at max_delay)

Jitter is added to prevent thundering herd when multiple requests fail simultaneously.

### Response Headers

When a request is retried, the response includes:
- `X-Retry-Count`: Number of retries performed

### Configuration Example

```bash
# Set retry configuration
export RETRY_MAX_RETRIES=3
export RETRY_INITIAL_DELAY_MS=100
export RETRY_MAX_DELAY_MS=5000
export RETRY_MULTIPLIER=2.0
export RETRY_JITTER_FACTOR=0.1
```

### Disabling Retries

To disable retries, set `RETRY_MAX_RETRIES=0`.

---

## Prometheus Metrics

The gateway exposes Prometheus-compatible metrics at `/metrics` endpoint.

### Available Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `gateway_uptime_seconds` | gauge | Time since gateway started |
| `gateway_requests_in_flight` | gauge | Current number of requests being processed |
| `gateway_rate_limited_total` | counter | Total number of rate limited requests |
| `gateway_http_requests_total` | counter | Total HTTP requests by method, path, status |
| `gateway_http_request_duration_ms` | summary | Request duration percentiles (p50, p95, p99) |
| `gateway_backend_requests_total` | counter | Total requests to backend services |
| `gateway_backend_errors_total` | counter | Total errors from backend services |
| `gateway_circuit_breaker_state` | gauge | Circuit breaker state (1=closed, 0.5=half-open, 0=open) |
| `gateway_circuit_breaker_trips_total` | counter | Total circuit breaker trips |

### Usage Examples

```bash
# Get metrics in Prometheus format
curl http://localhost:8081/metrics

# Get metrics in JSON format
curl -H "Accept: application/json" http://localhost:8081/metrics
```

### Prometheus Configuration

Add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'api-gateway'
    static_configs:
      - targets: ['localhost:8081']
    metrics_path: '/metrics'
    scrape_interval: 15s
```

### Grafana Dashboard

Key queries for Grafana dashboards:

```promql
# Request rate (per second)
rate(gateway_http_requests_total[5m])

# Error rate (5xx responses)
rate(gateway_http_requests_total{status=~"5.."}[5m])

# P95 latency
gateway_http_request_duration_ms{quantile="0.95"}

# Rate limited requests
rate(gateway_rate_limited_total[5m])

# Circuit breaker state
gateway_circuit_breaker_state
```

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
│   ├── circuitbreaker/
│   │   ├── circuitbreaker.go # Circuit breaker implementation
│   │   └── registry.go       # Circuit breaker registry
│   ├── handler/
│   │   └── handler.go        # HTTP handlers
│   ├── health/
│   │   └── health.go         # Health checker
│   ├── metrics/
│   │   └── metrics.go        # Prometheus metrics
│   ├── middleware/
│   │   └── middleware.go     # Middleware chain
│   ├── proxy/
│   │   └── proxy.go          # Reverse proxy
│   ├── ratelimit/
│   │   └── ratelimit.go      # Rate limiter
│   └── retry/
│       └── retry.go          # Retry with exponential backoff
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

## Testing

```bash
# Run all tests
go test ./...

# Run with verbose output
go test ./... -v

# Run specific package tests
go test ./internal/circuitbreaker/...
go test ./internal/retry/...
```

| Package | Tests | Coverage |
|---------|-------|----------|
| Circuit Breaker | 15 | State machine, transitions, concurrency, reset |
| Retry | 24 | Exponential backoff, jitter, transient errors |

## License

MIT License
