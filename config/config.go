package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Server   ServerConfig
	Redis    RedisConfig
	RateLimit RateLimitConfig
	Services []ServiceConfig
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

type ServiceConfig struct {
	Name       string
	PathPrefix string
	TargetURL  string
	StripPath  bool
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
			StripPath:  false,
		},
		{
			Name:       "user-service",
			PathPrefix: "/api/users",
			TargetURL:  getEnv("USER_SERVICE_URL", "http://localhost:8082"),
			StripPath:  false,
		},
	}
	return services
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
