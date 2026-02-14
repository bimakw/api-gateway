package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bimakw/api-gateway/config"
	"github.com/bimakw/api-gateway/internal/circuitbreaker"
	"github.com/bimakw/api-gateway/internal/loadbalancer"
	"github.com/bimakw/api-gateway/internal/metrics"
	"github.com/bimakw/api-gateway/internal/retry"
)

type ReverseProxy struct {
	services   map[string]*serviceProxy
	cbRegistry *circuitbreaker.Registry
	retryer    *retry.Retryer
	logger     *slog.Logger
	mu         sync.RWMutex
}

type serviceProxy struct {
	config       config.ServiceConfig
	loadBalancer *loadbalancer.LoadBalancer
	proxies      map[string]*httputil.ReverseProxy // key: backend URL string
}

func New(services []config.ServiceConfig, cbConfig circuitbreaker.Config, retryConfig retry.Config, logger *slog.Logger) (*ReverseProxy, error) {
	rp := &ReverseProxy{
		services:   make(map[string]*serviceProxy),
		cbRegistry: circuitbreaker.NewRegistry(cbConfig),
		retryer:    retry.New(retryConfig),
		logger:     logger,
	}

	for _, svc := range services {
		svcProxy, err := createServiceProxy(svc, logger)
		if err != nil {
			return nil, err
		}
		rp.services[svc.PathPrefix] = svcProxy

		logger.Info("Service configured",
			"service", svc.Name,
			"path", svc.PathPrefix,
			"backends", len(svcProxy.proxies),
			"strategy", svc.GetStrategy(),
		)
	}

	return rp, nil
}

func createServiceProxy(svc config.ServiceConfig, logger *slog.Logger) (*serviceProxy, error) {
	backendConfigs := svc.GetBackends()
	if len(backendConfigs) == 0 {
		return nil, nil
	}

	backends := make([]*loadbalancer.Backend, 0, len(backendConfigs))
	proxies := make(map[string]*httputil.ReverseProxy)

	for _, bc := range backendConfigs {
		targetURL, err := url.Parse(bc.URL)
		if err != nil {
			return nil, err
		}

		// Create backend for load balancer
		backend := &loadbalancer.Backend{
			URL:       targetURL,
			Weight:    bc.Weight,
			IsHealthy: true, // Start as healthy
		}
		backends = append(backends, backend)

		// Create reverse proxy for this backend
		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		// Customize the director to handle path manipulation
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)

			if svc.StripPath {
				req.URL.Path = strings.TrimPrefix(req.URL.Path, svc.PathPrefix)
				if req.URL.Path == "" {
					req.URL.Path = "/"
				}
			}

			req.Host = targetURL.Host
		}

		// Custom error handler
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Warn("Backend error",
				"service", svc.Name,
				"backend", targetURL.String(),
				"error", err.Error(),
			)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":"Service unavailable","message":"` + err.Error() + `"}`))
		}

		proxies[targetURL.String()] = proxy
	}

	lb := loadbalancer.New(svc.GetStrategy(), backends)

	return &serviceProxy{
		config:       svc,
		loadBalancer: lb,
		proxies:      proxies,
	}, nil
}

func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Find matching service
	for prefix, svc := range rp.services {
		if strings.HasPrefix(r.URL.Path, prefix) {
			rp.proxyWithRetry(w, r, svc)
			return
		}
	}

	// No matching service found
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":"Not found","message":"No service matches the requested path"}`))
}

func (rp *ReverseProxy) proxyWithRetry(w http.ResponseWriter, r *http.Request, svc *serviceProxy) {
	// Get circuit breaker for this service
	cb := rp.cbRegistry.Get(svc.config.Name)

	// Check if circuit is open
	if !cb.AllowRequest() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"Service unavailable","message":"Circuit breaker is open for ` + svc.config.Name + `"}`))
		return
	}

	// Select a healthy backend
	backend := svc.loadBalancer.Select()
	if backend == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"Service unavailable","message":"No healthy backends available for ` + svc.config.Name + `"}`))
		return
	}

	// Get proxy for selected backend
	proxy := svc.proxies[backend.URL.String()]
	if proxy == nil {
		rp.logger.Error("No proxy found for backend",
			"service", svc.config.Name,
			"backend", backend.URL.String(),
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"Internal error","message":"Backend proxy not found"}`))
		return
	}

	// Buffer request body for potential retries (only for methods with body)
	var bodyBytes []byte
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"Failed to read request body","message":"` + err.Error() + `"}`))
			return
		}
		r.Body.Close()
	}

	start := time.Now()
	var lastRecorder *retryableResponseRecorder
	attempt := 0
	selectedBackend := backend

	result := rp.retryer.Execute(r.Context(), func() (int, error) {
		attempt++

		// On retry, try to select a different backend if available
		if attempt > 1 {
			newBackend := svc.loadBalancer.Select()
			if newBackend != nil {
				selectedBackend = newBackend
				proxy = svc.proxies[selectedBackend.URL.String()]
			}
		}

		// Restore body for retry
		if bodyBytes != nil {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// Create a retryable response recorder
		lastRecorder = &retryableResponseRecorder{
			headers:    make(http.Header),
			body:       &bytes.Buffer{},
			statusCode: http.StatusOK,
		}

		// Execute proxy
		proxy.ServeHTTP(lastRecorder, r)

		// Log retry attempt
		if attempt > 1 {
			rp.logger.Info("retry attempt",
				"service", svc.config.Name,
				"backend", selectedBackend.URL.String(),
				"attempt", attempt,
				"status", lastRecorder.statusCode,
				"path", r.URL.Path,
			)
		}

		return lastRecorder.statusCode, nil
	})

	// Record metrics
	latency := time.Since(start)
	metrics.Get().RecordServiceRequest(svc.config.Name, result.StatusCode, latency)

	// Record circuit breaker result
	if result.StatusCode >= 500 {
		cb.RecordFailure()
	} else {
		cb.RecordSuccess()
	}

	// Write the final response
	if lastRecorder != nil {
		// Copy headers
		for key, values := range lastRecorder.headers {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		// Add retry info headers
		if result.Retried {
			w.Header().Set("X-Retry-Count", strconv.Itoa(result.Attempts-1))
		}

		// Add backend info header
		w.Header().Set("X-Backend", selectedBackend.URL.Host)

		w.WriteHeader(lastRecorder.statusCode)
		w.Write(lastRecorder.body.Bytes())
	}
}

// responseRecorder wraps http.ResponseWriter to capture status code
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// retryableResponseRecorder buffers the response for potential retries
type retryableResponseRecorder struct {
	headers    http.Header
	body       *bytes.Buffer
	statusCode int
}

func (r *retryableResponseRecorder) Header() http.Header {
	return r.headers
}

func (r *retryableResponseRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

func (r *retryableResponseRecorder) WriteHeader(code int) {
	r.statusCode = code
}

func (rp *ReverseProxy) GetServices() []config.ServiceConfig {
	services := make([]config.ServiceConfig, 0, len(rp.services))
	for _, svc := range rp.services {
		services = append(services, svc.config)
	}
	return services
}

func (rp *ReverseProxy) GetCircuitBreakerStats() []circuitbreaker.Stats {
	return rp.cbRegistry.GetAllStats()
}

func (rp *ReverseProxy) ResetCircuitBreaker(serviceName string) bool {
	return rp.cbRegistry.ResetByName(serviceName)
}

func (rp *ReverseProxy) ResetAllCircuitBreakers() {
	rp.cbRegistry.Reset()
}

func (rp *ReverseProxy) UpdateBackendHealth(serviceName, instanceURL string, healthy bool) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	for _, svc := range rp.services {
		if svc.config.Name == serviceName {
			svc.loadBalancer.SetHealthy(instanceURL, healthy)
			rp.logger.Debug("Backend health updated",
				"service", serviceName,
				"backend", instanceURL,
				"healthy", healthy,
			)
			return
		}
	}
}

func (rp *ReverseProxy) GetBackendStats(serviceName string) []BackendStats {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	for _, svc := range rp.services {
		if svc.config.Name == serviceName {
			backends := svc.loadBalancer.GetBackends()
			stats := make([]BackendStats, 0, len(backends))
			for _, b := range backends {
				stats = append(stats, BackendStats{
					URL:       b.URL.String(),
					IsHealthy: b.IsHealthy,
					Weight:    b.Weight,
				})
			}
			return stats
		}
	}
	return nil
}

// BackendStats contains statistics for a backend instance
type BackendStats struct {
	URL       string `json:"url"`
	IsHealthy bool   `json:"is_healthy"`
	Weight    int    `json:"weight"`
}

func (rp *ReverseProxy) GetAllBackendStats() map[string][]BackendStats {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	result := make(map[string][]BackendStats)
	for _, svc := range rp.services {
		backends := svc.loadBalancer.GetBackends()
		stats := make([]BackendStats, 0, len(backends))
		for _, b := range backends {
			stats = append(stats, BackendStats{
				URL:       b.URL.String(),
				IsHealthy: b.IsHealthy,
				Weight:    b.Weight,
			})
		}
		result[svc.config.Name] = stats
	}
	return result
}
