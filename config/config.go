package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server         ServerConfig
	Redis          RedisConfig
	RateLimit      RateLimitConfig
	CircuitBreaker CircuitBreakerConfig
	Retry          RetryConfig
	Admin          AdminConfig
	Services       []ServiceConfig
}

type CircuitBreakerConfig struct {
	MaxFailures         int
	ResetTimeoutSeconds int
	HalfOpenMaxRequests int
	SuccessThreshold    int
}

type RetryConfig struct {
	MaxRetries      int
	InitialDelayMs  int
	MaxDelayMs      int
	Multiplier      float64
	JitterFactor    float64
}

type AdminConfig struct {
	Username string
	Password string
	Enabled  bool
}

type ServerConfig struct {
	Host string
	Port string
}

type RedisConfig struct {
	Host     string
	Port     string
	Password string
	DB       int
}

type RateLimitConfig struct {
	RequestsPerMinute int
	BurstSize         int
	WindowDuration    time.Duration
}

// BackendConfig represents a single backend server
type BackendConfig struct {
	URL    string
	Weight int
}

type ServiceConfig struct {
	Name       string
	PathPrefix string
	TargetURL  string          // deprecated: use Backends for multiple instances
	Backends   []BackendConfig // multiple backend instances
	StripPath  bool
	Strategy   string // load balancing strategy: "round-robin", "random"
}

// GetBackends returns backend configurations, with backward compatibility
// If Backends is empty but TargetURL is set, returns single backend from TargetURL
func (s *ServiceConfig) GetBackends() []BackendConfig {
	if len(s.Backends) > 0 {
		return s.Backends
	}
	if s.TargetURL != "" {
		return []BackendConfig{{URL: s.TargetURL, Weight: 1}}
	}
	return nil
}

// GetStrategy returns the load balancing strategy, defaulting to round-robin
func (s *ServiceConfig) GetStrategy() string {
	if s.Strategy == "" {
		return "round-robin"
	}
	return s.Strategy
}

func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Host: getEnv("HOST", "0.0.0.0"),
			Port: getEnv("PORT", "8081"),
		},
		Redis: RedisConfig{
			Host:     getEnv("REDIS_HOST", "localhost"),
			Port:     getEnv("REDIS_PORT", "6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		RateLimit: RateLimitConfig{
			RequestsPerMinute: getEnvInt("RATE_LIMIT_RPM", 60),
			BurstSize:         getEnvInt("RATE_LIMIT_BURST", 10),
			WindowDuration:    time.Minute,
		},
		CircuitBreaker: CircuitBreakerConfig{
			MaxFailures:         getEnvInt("CB_MAX_FAILURES", 5),
			ResetTimeoutSeconds: getEnvInt("CB_RESET_TIMEOUT_SECONDS", 30),
			HalfOpenMaxRequests: getEnvInt("CB_HALF_OPEN_MAX_REQUESTS", 3),
			SuccessThreshold:    getEnvInt("CB_SUCCESS_THRESHOLD", 2),
		},
		Retry: RetryConfig{
			MaxRetries:     getEnvInt("RETRY_MAX_RETRIES", 3),
			InitialDelayMs: getEnvInt("RETRY_INITIAL_DELAY_MS", 100),
			MaxDelayMs:     getEnvInt("RETRY_MAX_DELAY_MS", 5000),
			Multiplier:     getEnvFloat("RETRY_MULTIPLIER", 2.0),
			JitterFactor:   getEnvFloat("RETRY_JITTER_FACTOR", 0.1),
		},
		Admin: AdminConfig{
			Username: getEnv("ADMIN_USERNAME", "admin"),
			Password: getEnv("ADMIN_PASSWORD", ""),
			Enabled:  getEnvBool("ADMIN_AUTH_ENABLED", true),
		},
		Services: loadServicesFromEnv(),
	}
}

func loadServicesFromEnv() []ServiceConfig {
	// Default services for demo
	services := []ServiceConfig{
		{
			Name:       "auth-service",
			PathPrefix: "/api/auth",
			TargetURL:  getEnv("AUTH_SERVICE_URL", "http://localhost:8080"),
			Backends:   parseBackendsEnv("AUTH_SERVICE_BACKENDS"),
			Strategy:   getEnv("AUTH_SERVICE_STRATEGY", "round-robin"),
			StripPath:  false,
		},
		{
			Name:       "user-service",
			PathPrefix: "/api/users",
			TargetURL:  getEnv("USER_SERVICE_URL", "http://localhost:8082"),
			Backends:   parseBackendsEnv("USER_SERVICE_BACKENDS"),
			Strategy:   getEnv("USER_SERVICE_STRATEGY", "round-robin"),
			StripPath:  false,
		},
	}
	return services
}

// parseBackendsEnv parses comma-separated backend URLs from environment variable
// Format: URL1,URL2,URL3 or URL1:weight1,URL2:weight2
func parseBackendsEnv(key string) []BackendConfig {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	backends := make([]BackendConfig, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Check if weight is specified (format: URL:weight)
		if idx := strings.LastIndex(part, ":"); idx > 0 {
			// Check if it's a port number or weight
			// Weight format would be after the last colon and only digits
			suffix := part[idx+1:]
			if weight, err := strconv.Atoi(suffix); err == nil && !strings.Contains(suffix, "/") {
				// It might be a weight, but we need to check if there's a port before
				// Check if removing the suffix leaves a valid URL with port
				potentialURL := part[:idx]
				if strings.Count(potentialURL, ":") >= 1 {
					// Has at least one colon (scheme:// or host:port), so last part is weight
					backends = append(backends, BackendConfig{
						URL:    potentialURL,
						Weight: weight,
					})
					continue
				}
			}
		}

		// No weight specified or parsing failed, use default weight
		backends = append(backends, BackendConfig{
			URL:    part,
			Weight: 1,
		})
	}

	return backends
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}
