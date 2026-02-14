package middleware

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bimakw/api-gateway/internal/apikey"
	"github.com/bimakw/api-gateway/internal/metrics"
	"github.com/bimakw/api-gateway/internal/ratelimit"
)

type contextKey string

const (
	APIKeyContextKey contextKey = "api_key"
	RequestIDKey     contextKey = "request_id"
)

// Middleware is a function that wraps an http.Handler
type Middleware func(http.Handler) http.Handler

// Chain applies multiple middlewares to a handler
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// Logger logs request details
func Logger(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.statusCode,
				"duration_ms", duration.Milliseconds(),
				"client_ip", getClientIP(r),
				"user_agent", r.UserAgent(),
			)
		})
	}
}

func Metrics() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip metrics endpoint itself to avoid recursion
			if r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}

			m := metrics.Get()
			start := time.Now()

			// Track in-flight requests
			m.IncrementInFlight()
			defer m.DecrementInFlight()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)

			// Record request metrics
			m.RecordRequest(r.Method, r.URL.Path, wrapped.statusCode, duration)
		})
	}
}

// RateLimit applies rate limiting based on IP or API key
func RateLimit(limiter *ratelimit.RateLimiter, burstSize int) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Use API key if present, otherwise use IP
			key := getClientIP(r)
			if apiKey, ok := r.Context().Value(APIKeyContextKey).(*apikey.APIKey); ok {
				key = "apikey:" + apiKey.ID
			}

			result, err := limiter.AllowWithBurst(r.Context(), key, burstSize)
			if err != nil {
				http.Error(w, `{"error":"Internal server error"}`, http.StatusInternalServerError)
				return
			}

			// Set rate limit headers
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(result.ResetAfter).Unix(), 10))

			if !result.Allowed {
				// Record rate limited request in metrics
				metrics.Get().IncrementRateLimited()

				w.Header().Set("Retry-After", strconv.Itoa(int(result.ResetAfter.Seconds())))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"Rate limit exceeded","message":"Too many requests, please try again later"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// APIKeyAuth validates API key from header
func APIKeyAuth(manager *apikey.Manager, required bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check for API key in header
			rawKey := r.Header.Get("X-API-Key")
			if rawKey == "" {
				// Also check Authorization header with Bearer prefix
				auth := r.Header.Get("Authorization")
				if strings.HasPrefix(auth, "Bearer ") {
					rawKey = strings.TrimPrefix(auth, "Bearer ")
				}
			}

			if rawKey == "" {
				if required {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					w.Write([]byte(`{"error":"Unauthorized","message":"API key required"}`))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Validate the key
			apiKey, err := manager.ValidateKey(r.Context(), rawKey)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"Unauthorized","message":"` + err.Error() + `"}`))
				return
			}

			// Add API key to context
			ctx := context.WithValue(r.Context(), APIKeyContextKey, apiKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CORS adds CORS headers
func CORS(allowedOrigins []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Check if origin is allowed
			allowed := false
			for _, o := range allowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
				w.Header().Set("Access-Control-Max-Age", "3600")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Recover recovers from panics
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered", "error", err, "path", r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"error":"Internal server error"}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AdminAuth provides Basic Authentication for admin endpoints
// Uses constant-time comparison to prevent timing attacks
func AdminAuth(username, password string, logger *slog.Logger) Middleware {
	// Pre-compute hashes for constant-time comparison
	expectedUsernameHash := sha256.Sum256([]byte(username))
	expectedPasswordHash := sha256.Sum256([]byte(password))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only protect /admin/* paths
			if !strings.HasPrefix(r.URL.Path, "/admin") {
				next.ServeHTTP(w, r)
				return
			}

			// Get Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				adminAuthFailed(w, "Authorization header required")
				return
			}

			// Check for Basic auth prefix
			if !strings.HasPrefix(authHeader, "Basic ") {
				adminAuthFailed(w, "Basic authentication required")
				return
			}

			// Decode base64 credentials
			encoded := strings.TrimPrefix(authHeader, "Basic ")
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				adminAuthFailed(w, "Invalid authorization header")
				return
			}

			// Split username:password
			credentials := string(decoded)
			colonIdx := strings.Index(credentials, ":")
			if colonIdx == -1 {
				adminAuthFailed(w, "Invalid credentials format")
				return
			}

			providedUsername := credentials[:colonIdx]
			providedPassword := credentials[colonIdx+1:]

			// Constant-time comparison to prevent timing attacks
			providedUsernameHash := sha256.Sum256([]byte(providedUsername))
			providedPasswordHash := sha256.Sum256([]byte(providedPassword))

			usernameMatch := subtle.ConstantTimeCompare(providedUsernameHash[:], expectedUsernameHash[:]) == 1
			passwordMatch := subtle.ConstantTimeCompare(providedPasswordHash[:], expectedPasswordHash[:]) == 1

			if !usernameMatch || !passwordMatch {
				logger.Warn("admin auth failed",
					"client_ip", getClientIP(r),
					"path", r.URL.Path,
				)
				adminAuthFailed(w, "Invalid credentials")
				return
			}

			// Authentication successful
			next.ServeHTTP(w, r)
		})
	}
}

func adminAuthFailed(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="API Gateway Admin", charset="UTF-8"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"Unauthorized","message":"` + message + `"}`))
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	ip := r.RemoteAddr
	if colonIndex := strings.LastIndex(ip, ":"); colonIndex != -1 {
		ip = ip[:colonIndex]
	}
	return ip
}
