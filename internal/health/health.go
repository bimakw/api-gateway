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

// InstanceHealth represents health status of a single backend instance
type InstanceHealth struct {
	URL          string    `json:"url"`
	Status       Status    `json:"status"`
	LastCheck    time.Time `json:"last_check"`
	ResponseTime int64     `json:"response_time_ms"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

// ServiceHealth represents aggregated health status of a service with all its instances
type ServiceHealth struct {
	Name         string            `json:"name"`
	URL          string            `json:"url"`                    // Primary URL (for backward compatibility)
	Status       Status            `json:"status"`                 // Aggregated status
	Instances    []*InstanceHealth `json:"instances,omitempty"`    // Per-instance health
	LastCheck    time.Time         `json:"last_check"`
	ResponseTime int64             `json:"response_time_ms"`       // Average response time
	ErrorMessage string            `json:"error_message,omitempty"`
}

// HealthCallback is called when instance health status changes
type HealthCallback func(serviceName, instanceURL string, healthy bool)

type Checker struct {
	services    []config.ServiceConfig
	healthMap   map[string]*ServiceHealth
	instanceMap map[string]map[string]*InstanceHealth // serviceName -> instanceURL -> health
	mu          sync.RWMutex
	interval    time.Duration
	timeout     time.Duration
	client      *http.Client
	logger      *slog.Logger
	stopCh      chan struct{}
	callbacks   []HealthCallback
	callbackMu  sync.RWMutex
}

func NewChecker(services []config.ServiceConfig, interval, timeout time.Duration, logger *slog.Logger) *Checker {
	healthMap := make(map[string]*ServiceHealth)
	instanceMap := make(map[string]map[string]*InstanceHealth)

	for _, svc := range services {
		backends := svc.GetBackends()
		instances := make([]*InstanceHealth, 0, len(backends))
		instanceURLMap := make(map[string]*InstanceHealth)

		// Get primary URL for backward compatibility
		primaryURL := svc.TargetURL
		if len(backends) > 0 {
			primaryURL = backends[0].URL
		}

		for _, backend := range backends {
			instance := &InstanceHealth{
				URL:    backend.URL,
				Status: StatusUnknown,
			}
			instances = append(instances, instance)
			instanceURLMap[backend.URL] = instance
		}

		healthMap[svc.Name] = &ServiceHealth{
			Name:      svc.Name,
			URL:       primaryURL,
			Status:    StatusUnknown,
			Instances: instances,
		}
		instanceMap[svc.Name] = instanceURLMap
	}

	return &Checker{
		services:    services,
		healthMap:   healthMap,
		instanceMap: instanceMap,
		interval:    interval,
		timeout:     timeout,
		client: &http.Client{
			Timeout: timeout,
		},
		logger:    logger,
		stopCh:    make(chan struct{}),
		callbacks: make([]HealthCallback, 0),
	}
}

// RegisterCallback registers a callback to be called when instance health changes
func (c *Checker) RegisterCallback(cb HealthCallback) {
	c.callbackMu.Lock()
	defer c.callbackMu.Unlock()
	c.callbacks = append(c.callbacks, cb)
}

// notifyCallbacks notifies all registered callbacks of a health change
func (c *Checker) notifyCallbacks(serviceName, instanceURL string, healthy bool) {
	c.callbackMu.RLock()
	callbacks := make([]HealthCallback, len(c.callbacks))
	copy(callbacks, c.callbacks)
	c.callbackMu.RUnlock()

	for _, cb := range callbacks {
		cb(serviceName, instanceURL, healthy)
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
		backends := svc.GetBackends()
		for _, backend := range backends {
			wg.Add(1)
			go func(svc config.ServiceConfig, backendURL string) {
				defer wg.Done()
				c.checkInstance(ctx, svc.Name, backendURL)
			}(svc, backend.URL)
		}
	}

	wg.Wait()

	// Update aggregated service health after all instances are checked
	c.updateAggregatedHealth()
}

// checkInstance checks health of a single backend instance
func (c *Checker) checkInstance(ctx context.Context, serviceName, instanceURL string) {
	start := time.Now()
	healthURL := instanceURL + "/health"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		c.updateInstanceHealth(serviceName, instanceURL, StatusUnhealthy, 0, err.Error())
		return
	}

	resp, err := c.client.Do(req)
	responseTime := time.Since(start).Milliseconds()

	if err != nil {
		c.logger.Warn("Health check failed",
			"service", serviceName,
			"instance", instanceURL,
			"error", err.Error(),
		)
		c.updateInstanceHealth(serviceName, instanceURL, StatusUnhealthy, responseTime, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.updateInstanceHealth(serviceName, instanceURL, StatusHealthy, responseTime, "")
		c.logger.Debug("Health check passed",
			"service", serviceName,
			"instance", instanceURL,
			"status", resp.StatusCode,
			"response_time_ms", responseTime,
		)
	} else {
		c.updateInstanceHealth(serviceName, instanceURL, StatusUnhealthy, responseTime, "unhealthy status code: "+resp.Status)
		c.logger.Warn("Health check failed",
			"service", serviceName,
			"instance", instanceURL,
			"status", resp.StatusCode,
		)
	}
}

func (c *Checker) updateInstanceHealth(serviceName, instanceURL string, status Status, responseTime int64, errorMsg string) {
	c.mu.Lock()

	instanceURLMap, ok := c.instanceMap[serviceName]
	if !ok {
		c.mu.Unlock()
		return
	}

	instance, ok := instanceURLMap[instanceURL]
	if !ok {
		c.mu.Unlock()
		return
	}

	// Check if status changed
	previousStatus := instance.Status
	statusChanged := previousStatus != status

	instance.Status = status
	instance.LastCheck = time.Now()
	instance.ResponseTime = responseTime
	instance.ErrorMessage = errorMsg

	c.mu.Unlock()

	// Notify callbacks if status changed
	if statusChanged {
		c.notifyCallbacks(serviceName, instanceURL, status == StatusHealthy)
	}
}

// updateAggregatedHealth updates the aggregated health for each service
func (c *Checker) updateAggregatedHealth() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for serviceName, health := range c.healthMap {
		instanceMap := c.instanceMap[serviceName]
		if len(instanceMap) == 0 {
			continue
		}

		// Collect instance health
		instances := make([]*InstanceHealth, 0, len(instanceMap))
		healthyCount := 0
		totalResponseTime := int64(0)
		var latestCheck time.Time
		var latestError string

		for _, instance := range instanceMap {
			instances = append(instances, &InstanceHealth{
				URL:          instance.URL,
				Status:       instance.Status,
				LastCheck:    instance.LastCheck,
				ResponseTime: instance.ResponseTime,
				ErrorMessage: instance.ErrorMessage,
			})

			if instance.Status == StatusHealthy {
				healthyCount++
			} else if instance.ErrorMessage != "" {
				latestError = instance.ErrorMessage
			}

			totalResponseTime += instance.ResponseTime
			if instance.LastCheck.After(latestCheck) {
				latestCheck = instance.LastCheck
			}
		}

		// Determine aggregated status
		var aggregatedStatus Status
		if healthyCount == len(instanceMap) {
			aggregatedStatus = StatusHealthy
			latestError = ""
		} else if healthyCount > 0 {
			aggregatedStatus = StatusHealthy // Partially healthy is still healthy (at least one backend works)
		} else {
			aggregatedStatus = StatusUnhealthy
		}

		health.Status = aggregatedStatus
		health.Instances = instances
		health.LastCheck = latestCheck
		health.ResponseTime = totalResponseTime / int64(len(instanceMap))
		health.ErrorMessage = latestError
	}
}

// Legacy methods for backward compatibility

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

// checkService checks health of a single service (legacy - checks first backend only)
func (c *Checker) checkService(ctx context.Context, svc config.ServiceConfig) {
	backends := svc.GetBackends()
	if len(backends) == 0 {
		return
	}
	c.checkInstance(ctx, svc.Name, backends[0].URL)
}

// GetHealth returns health status of a specific service
func (c *Checker) GetHealth(name string) *ServiceHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if health, ok := c.healthMap[name]; ok {
		// Return a copy to avoid race conditions
		instances := make([]*InstanceHealth, len(health.Instances))
		for i, inst := range health.Instances {
			instances[i] = &InstanceHealth{
				URL:          inst.URL,
				Status:       inst.Status,
				LastCheck:    inst.LastCheck,
				ResponseTime: inst.ResponseTime,
				ErrorMessage: inst.ErrorMessage,
			}
		}
		return &ServiceHealth{
			Name:         health.Name,
			URL:          health.URL,
			Status:       health.Status,
			Instances:    instances,
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
		instances := make([]*InstanceHealth, len(health.Instances))
		for i, inst := range health.Instances {
			instances[i] = &InstanceHealth{
				URL:          inst.URL,
				Status:       inst.Status,
				LastCheck:    inst.LastCheck,
				ResponseTime: inst.ResponseTime,
				ErrorMessage: inst.ErrorMessage,
			}
		}
		result = append(result, &ServiceHealth{
			Name:         health.Name,
			URL:          health.URL,
			Status:       health.Status,
			Instances:    instances,
			LastCheck:    health.LastCheck,
			ResponseTime: health.ResponseTime,
			ErrorMessage: health.ErrorMessage,
		})
	}
	return result
}

// IsHealthy checks if a service is healthy (at least one instance healthy)
func (c *Checker) IsHealthy(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if health, ok := c.healthMap[name]; ok {
		return health.Status == StatusHealthy
	}
	return false
}

// GetInstanceHealth returns health of a specific instance
func (c *Checker) GetInstanceHealth(serviceName, instanceURL string) *InstanceHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if instanceMap, ok := c.instanceMap[serviceName]; ok {
		if instance, ok := instanceMap[instanceURL]; ok {
			return &InstanceHealth{
				URL:          instance.URL,
				Status:       instance.Status,
				LastCheck:    instance.LastCheck,
				ResponseTime: instance.ResponseTime,
				ErrorMessage: instance.ErrorMessage,
			}
		}
	}
	return nil
}
