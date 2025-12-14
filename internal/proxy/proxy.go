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
	"time"

	"github.com/bimakw/api-gateway/config"
	"github.com/bimakw/api-gateway/internal/circuitbreaker"
	"github.com/bimakw/api-gateway/internal/metrics"
	"github.com/bimakw/api-gateway/internal/retry"
)

type ReverseProxy struct {
	services   map[string]*serviceProxy
	cbRegistry *circuitbreaker.Registry
	retryer    *retry.Retryer
	logger     *slog.Logger
}

type serviceProxy struct {
	config config.ServiceConfig
	proxy  *httputil.ReverseProxy
}

func New(services []config.ServiceConfig, cbConfig circuitbreaker.Config, retryConfig retry.Config, logger *slog.Logger) (*ReverseProxy, error) {
	rp := &ReverseProxy{
		services:   make(map[string]*serviceProxy),
		cbRegistry: circuitbreaker.NewRegistry(cbConfig),
		retryer:    retry.New(retryConfig),
		logger:     logger,
	}

	for _, svc := range services {
		targetURL, err := url.Parse(svc.TargetURL)
		if err != nil {
			return nil, err
		}

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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":"Service unavailable","message":"` + err.Error() + `"}`))
		}

		rp.services[svc.PathPrefix] = &serviceProxy{
			config: svc,
			proxy:  proxy,
		}
	}

	return rp, nil
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

// proxyWithRetry handles the proxy request with retry logic
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

	result := rp.retryer.Execute(r.Context(), func() (int, error) {
		attempt++

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
		svc.proxy.ServeHTTP(lastRecorder, r)

		// Log retry attempt
		if attempt > 1 {
			rp.logger.Info("retry attempt",
				"service", svc.config.Name,
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

// GetCircuitBreakerStats returns statistics for all circuit breakers
func (rp *ReverseProxy) GetCircuitBreakerStats() []circuitbreaker.Stats {
	return rp.cbRegistry.GetAllStats()
}

// ResetCircuitBreaker resets a specific circuit breaker
func (rp *ReverseProxy) ResetCircuitBreaker(serviceName string) bool {
	return rp.cbRegistry.ResetByName(serviceName)
}

// ResetAllCircuitBreakers resets all circuit breakers
func (rp *ReverseProxy) ResetAllCircuitBreakers() {
	rp.cbRegistry.Reset()
}
