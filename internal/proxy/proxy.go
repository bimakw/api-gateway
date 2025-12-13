package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/bimakw/api-gateway/config"
	"github.com/bimakw/api-gateway/internal/circuitbreaker"
)

type ReverseProxy struct {
	services map[string]*serviceProxy
	cbRegistry *circuitbreaker.Registry
}

type serviceProxy struct {
	config config.ServiceConfig
	proxy  *httputil.ReverseProxy
}

func New(services []config.ServiceConfig, cbConfig circuitbreaker.Config) (*ReverseProxy, error) {
	rp := &ReverseProxy{
		services:   make(map[string]*serviceProxy),
		cbRegistry: circuitbreaker.NewRegistry(cbConfig),
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
			// Get circuit breaker for this service
			cb := rp.cbRegistry.Get(svc.config.Name)

			// Check if circuit is open
			if !cb.AllowRequest() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"error":"Service unavailable","message":"Circuit breaker is open for ` + svc.config.Name + `"}`))
				return
			}

			// Create a response recorder to capture the status code
			recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			svc.proxy.ServeHTTP(recorder, r)

			// Record success or failure based on status code
			if recorder.statusCode >= 500 {
				cb.RecordFailure()
			} else {
				cb.RecordSuccess()
			}
			return
		}
	}

	// No matching service found
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":"Not found","message":"No service matches the requested path"}`))
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
