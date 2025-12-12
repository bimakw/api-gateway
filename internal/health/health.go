package health

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/bimakw/api-gateway/config"
)

type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
	StatusUnknown   Status = "unknown"
)

type ServiceHealth struct {
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	Status       Status    `json:"status"`
	LastCheck    time.Time `json:"last_check"`
	ResponseTime int64     `json:"response_time_ms"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

type Checker struct {
	services    []config.ServiceConfig
	healthMap   map[string]*ServiceHealth
	mu          sync.RWMutex
	interval    time.Duration
	timeout     time.Duration
	client      *http.Client
	logger      *slog.Logger
	stopCh      chan struct{}
}

func NewChecker(services []config.ServiceConfig, interval, timeout time.Duration, logger *slog.Logger) *Checker {
	healthMap := make(map[string]*ServiceHealth)
	for _, svc := range services {
		healthMap[svc.Name] = &ServiceHealth{
			Name:   svc.Name,
			URL:    svc.TargetURL,
			Status: StatusUnknown,
		}
	}

	return &Checker{
		services:  services,
		healthMap: healthMap,
		interval:  interval,
		timeout:   timeout,
		client: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

// Start begins the periodic health checking
func (c *Checker) Start(ctx context.Context) {
	// Run initial check
	c.checkAll(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.checkAll(ctx)
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Stop stops the health checker
func (c *Checker) Stop() {
	close(c.stopCh)
}

// checkAll checks health of all services concurrently
func (c *Checker) checkAll(ctx context.Context) {
	var wg sync.WaitGroup

	for _, svc := range c.services {
		wg.Add(1)
		go func(svc config.ServiceConfig) {
			defer wg.Done()
			c.checkService(ctx, svc)
		}(svc)
	}

	wg.Wait()
}

// checkService checks health of a single service
func (c *Checker) checkService(ctx context.Context, svc config.ServiceConfig) {
	start := time.Now()
	healthURL := svc.TargetURL + "/health"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		c.updateHealth(svc.Name, StatusUnhealthy, 0, err.Error())
		return
	}

	resp, err := c.client.Do(req)
	responseTime := time.Since(start).Milliseconds()

	if err != nil {
		c.logger.Warn("Health check failed",
			"service", svc.Name,
			"url", healthURL,
			"error", err.Error(),
		)
		c.updateHealth(svc.Name, StatusUnhealthy, responseTime, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.updateHealth(svc.Name, StatusHealthy, responseTime, "")
		c.logger.Debug("Health check passed",
			"service", svc.Name,
			"status", resp.StatusCode,
			"response_time_ms", responseTime,
		)
	} else {
		c.updateHealth(svc.Name, StatusUnhealthy, responseTime, "unhealthy status code: "+resp.Status)
		c.logger.Warn("Health check failed",
			"service", svc.Name,
			"status", resp.StatusCode,
		)
	}
}

func (c *Checker) updateHealth(name string, status Status, responseTime int64, errorMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if health, ok := c.healthMap[name]; ok {
		health.Status = status
		health.LastCheck = time.Now()
		health.ResponseTime = responseTime
		health.ErrorMessage = errorMsg
	}
}

// GetHealth returns health status of a specific service
func (c *Checker) GetHealth(name string) *ServiceHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if health, ok := c.healthMap[name]; ok {
		// Return a copy to avoid race conditions
		return &ServiceHealth{
			Name:         health.Name,
			URL:          health.URL,
			Status:       health.Status,
			LastCheck:    health.LastCheck,
			ResponseTime: health.ResponseTime,
			ErrorMessage: health.ErrorMessage,
		}
	}
	return nil
}

// GetAllHealth returns health status of all services
func (c *Checker) GetAllHealth() []*ServiceHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*ServiceHealth, 0, len(c.healthMap))
	for _, health := range c.healthMap {
		result = append(result, &ServiceHealth{
			Name:         health.Name,
			URL:          health.URL,
			Status:       health.Status,
			LastCheck:    health.LastCheck,
			ResponseTime: health.ResponseTime,
			ErrorMessage: health.ErrorMessage,
		})
	}
	return result
}

// IsHealthy checks if a service is healthy
func (c *Checker) IsHealthy(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if health, ok := c.healthMap[name]; ok {
		return health.Status == StatusHealthy
	}
	return false
}
