/*
 * Copyright (c) 2024 Bima Kharisma Wicaksana
 * GitHub: https://github.com/bimakw
 *
 * Licensed under MIT License with Attribution Requirement.
 * See LICENSE file for details.
 */

package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Metrics holds all the prometheus-style metrics for the API Gateway
type Metrics struct {
	mu sync.RWMutex

	// Request counters
	requestsTotal     map[string]int64 // key: "method:path:status"
	requestsInFlight  int64
	requestDurations  []durationRecord

	// Rate limiter metrics
	rateLimitedTotal int64

	// Circuit breaker metrics
	circuitBreakerState   map[string]string // service -> state
	circuitBreakerTrips   map[string]int64  // service -> trip count

	// Service metrics
	serviceRequestsTotal    map[string]int64 // service -> count
	serviceErrorsTotal      map[string]int64 // service -> error count
	serviceLatencies        map[string][]float64 // service -> latencies in ms

	startTime time.Time
}

type durationRecord struct {
	method   string
	path     string
	status   int
	duration float64 // in seconds
	time     time.Time
}

// Global metrics instance
var (
	instance *Metrics
	once     sync.Once
)

// Get returns the singleton metrics instance
func Get() *Metrics {
	once.Do(func() {
		instance = &Metrics{
			requestsTotal:         make(map[string]int64),
			requestDurations:      make([]durationRecord, 0),
			circuitBreakerState:   make(map[string]string),
			circuitBreakerTrips:   make(map[string]int64),
			serviceRequestsTotal:  make(map[string]int64),
			serviceErrorsTotal:    make(map[string]int64),
			serviceLatencies:      make(map[string][]float64),
			startTime:             time.Now(),
		}
	})
	return instance
}

// RecordRequest records a completed HTTP request
func (m *Metrics) RecordRequest(method, path string, status int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Normalize path for metrics (remove IDs, etc)
	normalizedPath := normalizePath(path)

	key := method + ":" + normalizedPath + ":" + strconv.Itoa(status)
	m.requestsTotal[key]++

	// Keep last 1000 duration records for percentile calculation
	record := durationRecord{
		method:   method,
		path:     normalizedPath,
		status:   status,
		duration: duration.Seconds(),
		time:     time.Now(),
	}
	m.requestDurations = append(m.requestDurations, record)
	if len(m.requestDurations) > 1000 {
		m.requestDurations = m.requestDurations[1:]
	}
}

// RecordServiceRequest records a request to a backend service
func (m *Metrics) RecordServiceRequest(serviceName string, status int, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.serviceRequestsTotal[serviceName]++

	if status >= 500 {
		m.serviceErrorsTotal[serviceName]++
	}

	latencies := m.serviceLatencies[serviceName]
	latencies = append(latencies, float64(latency.Milliseconds()))
	// Keep last 100 latencies per service
	if len(latencies) > 100 {
		latencies = latencies[1:]
	}
	m.serviceLatencies[serviceName] = latencies
}

// IncrementRateLimited increments the rate limited counter
func (m *Metrics) IncrementRateLimited() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rateLimitedTotal++
}

// IncrementInFlight increments requests in flight
func (m *Metrics) IncrementInFlight() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestsInFlight++
}

// DecrementInFlight decrements requests in flight
func (m *Metrics) DecrementInFlight() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestsInFlight--
}

// UpdateCircuitBreakerState updates the circuit breaker state for a service
func (m *Metrics) UpdateCircuitBreakerState(serviceName, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.circuitBreakerState[serviceName] = state
}

// IncrementCircuitBreakerTrips increments the circuit breaker trip counter
func (m *Metrics) IncrementCircuitBreakerTrips(serviceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.circuitBreakerTrips[serviceName]++
}

// GetMetricsData returns all metrics data for the handler
func (m *Metrics) GetMetricsData() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Calculate request totals by status code
	statusCounts := make(map[int]int64)
	methodCounts := make(map[string]int64)
	for key, count := range m.requestsTotal {
		parts := splitKey(key)
		if len(parts) >= 3 {
			if status, err := strconv.Atoi(parts[2]); err == nil {
				statusCounts[status] += count
			}
			methodCounts[parts[0]] += count
		}
	}

	// Calculate latency percentiles from recent records
	var allDurations []float64
	for _, r := range m.requestDurations {
		allDurations = append(allDurations, r.duration*1000) // convert to ms
	}

	p50, p95, p99 := calculatePercentiles(allDurations)

	// Calculate per-service average latency
	serviceAvgLatency := make(map[string]float64)
	for svc, latencies := range m.serviceLatencies {
		if len(latencies) > 0 {
			var sum float64
			for _, l := range latencies {
				sum += l
			}
			serviceAvgLatency[svc] = sum / float64(len(latencies))
		}
	}

	// Total requests
	var totalRequests int64
	for _, count := range m.requestsTotal {
		totalRequests += count
	}

	return map[string]interface{}{
		"uptime_seconds":       time.Since(m.startTime).Seconds(),
		"requests_total":       totalRequests,
		"requests_in_flight":   m.requestsInFlight,
		"rate_limited_total":   m.rateLimitedTotal,
		"requests_by_status":   statusCounts,
		"requests_by_method":   methodCounts,
		"latency_p50_ms":       p50,
		"latency_p95_ms":       p95,
		"latency_p99_ms":       p99,
		"circuit_breakers":     m.circuitBreakerState,
		"circuit_breaker_trips": m.circuitBreakerTrips,
		"service_requests":     m.serviceRequestsTotal,
		"service_errors":       m.serviceErrorsTotal,
		"service_avg_latency_ms": serviceAvgLatency,
	}
}

// GetPrometheusFormat returns metrics in Prometheus text format
func (m *Metrics) GetPrometheusFormat() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result string

	// Uptime
	result += "# HELP gateway_uptime_seconds Time since gateway started\n"
	result += "# TYPE gateway_uptime_seconds gauge\n"
	result += "gateway_uptime_seconds " + formatFloat(time.Since(m.startTime).Seconds()) + "\n\n"

	// Requests in flight
	result += "# HELP gateway_requests_in_flight Current number of requests being processed\n"
	result += "# TYPE gateway_requests_in_flight gauge\n"
	result += "gateway_requests_in_flight " + strconv.FormatInt(m.requestsInFlight, 10) + "\n\n"

	// Rate limited total
	result += "# HELP gateway_rate_limited_total Total number of rate limited requests\n"
	result += "# TYPE gateway_rate_limited_total counter\n"
	result += "gateway_rate_limited_total " + strconv.FormatInt(m.rateLimitedTotal, 10) + "\n\n"

	// Requests total by method, path, status
	result += "# HELP gateway_http_requests_total Total number of HTTP requests\n"
	result += "# TYPE gateway_http_requests_total counter\n"
	for key, count := range m.requestsTotal {
		parts := splitKey(key)
		if len(parts) >= 3 {
			result += "gateway_http_requests_total{method=\"" + parts[0] + "\",path=\"" + parts[1] + "\",status=\"" + parts[2] + "\"} " + strconv.FormatInt(count, 10) + "\n"
		}
	}
	result += "\n"

	// Request duration histogram approximation (using percentiles)
	var allDurations []float64
	for _, r := range m.requestDurations {
		allDurations = append(allDurations, r.duration*1000)
	}
	p50, p95, p99 := calculatePercentiles(allDurations)

	result += "# HELP gateway_http_request_duration_ms HTTP request duration in milliseconds\n"
	result += "# TYPE gateway_http_request_duration_ms summary\n"
	result += "gateway_http_request_duration_ms{quantile=\"0.5\"} " + formatFloat(p50) + "\n"
	result += "gateway_http_request_duration_ms{quantile=\"0.95\"} " + formatFloat(p95) + "\n"
	result += "gateway_http_request_duration_ms{quantile=\"0.99\"} " + formatFloat(p99) + "\n\n"

	// Service metrics
	result += "# HELP gateway_backend_requests_total Total requests to backend services\n"
	result += "# TYPE gateway_backend_requests_total counter\n"
	for svc, count := range m.serviceRequestsTotal {
		result += "gateway_backend_requests_total{service=\"" + svc + "\"} " + strconv.FormatInt(count, 10) + "\n"
	}
	result += "\n"

	result += "# HELP gateway_backend_errors_total Total errors from backend services\n"
	result += "# TYPE gateway_backend_errors_total counter\n"
	for svc, count := range m.serviceErrorsTotal {
		result += "gateway_backend_errors_total{service=\"" + svc + "\"} " + strconv.FormatInt(count, 10) + "\n"
	}
	result += "\n"

	// Circuit breaker state (1 = closed, 0.5 = half-open, 0 = open)
	result += "# HELP gateway_circuit_breaker_state Circuit breaker state (1=closed, 0.5=half-open, 0=open)\n"
	result += "# TYPE gateway_circuit_breaker_state gauge\n"
	for svc, state := range m.circuitBreakerState {
		stateValue := "1"
		switch state {
		case "open":
			stateValue = "0"
		case "half-open":
			stateValue = "0.5"
		}
		result += "gateway_circuit_breaker_state{service=\"" + svc + "\"} " + stateValue + "\n"
	}
	result += "\n"

	result += "# HELP gateway_circuit_breaker_trips_total Total circuit breaker trips\n"
	result += "# TYPE gateway_circuit_breaker_trips_total counter\n"
	for svc, count := range m.circuitBreakerTrips {
		result += "gateway_circuit_breaker_trips_total{service=\"" + svc + "\"} " + strconv.FormatInt(count, 10) + "\n"
	}

	return result
}

// Handler returns an http.HandlerFunc for the /metrics endpoint
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		m := Get()

		if accept == "application/json" {
			w.Header().Set("Content-Type", "application/json")
			data := m.GetMetricsData()
			writeJSON(w, data)
			return
		}

		// Default to Prometheus format
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write([]byte(m.GetPrometheusFormat()))
	}
}

// Helper functions

func normalizePath(path string) string {
	// Simple normalization: replace UUIDs and numeric IDs with placeholder
	// This keeps cardinality low for metrics
	segments := []byte(path)
	result := make([]byte, 0, len(segments))

	i := 0
	for i < len(segments) {
		if segments[i] == '/' {
			result = append(result, segments[i])
			i++
			// Check if next segment looks like an ID
			start := i
			for i < len(segments) && segments[i] != '/' {
				i++
			}
			segment := string(segments[start:i])
			if looksLikeID(segment) {
				result = append(result, []byte(":id")...)
			} else {
				result = append(result, []byte(segment)...)
			}
		} else {
			result = append(result, segments[i])
			i++
		}
	}

	return string(result)
}

func looksLikeID(s string) bool {
	if len(s) == 0 {
		return false
	}

	// Check if all numeric
	allNumeric := true
	for _, c := range s {
		if c < '0' || c > '9' {
			allNumeric = false
			break
		}
	}
	if allNumeric && len(s) > 0 {
		return true
	}

	// Check if looks like UUID (36 chars with hyphens)
	if len(s) == 36 {
		for i, c := range s {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				if c != '-' {
					return false
				}
			} else {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
					return false
				}
			}
		}
		return true
	}

	return false
}

func splitKey(key string) []string {
	var result []string
	var current []byte
	for _, c := range []byte(key) {
		if c == ':' {
			result = append(result, string(current))
			current = nil
		} else {
			current = append(current, c)
		}
	}
	if len(current) > 0 {
		result = append(result, string(current))
	}
	return result
}

func calculatePercentiles(values []float64) (p50, p95, p99 float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}

	// Simple sorting for percentile calculation
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sortFloats(sorted)

	p50 = sorted[int(float64(len(sorted))*0.50)]
	p95 = sorted[int(float64(len(sorted)-1)*0.95)]
	p99 = sorted[int(float64(len(sorted)-1)*0.99)]

	return
}

func sortFloats(a []float64) {
	// Simple insertion sort (good enough for small datasets)
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', 3, 64)
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	// Simple JSON encoding without external deps
	if m, ok := data.(map[string]interface{}); ok {
		w.Write([]byte("{"))
		first := true
		for k, v := range m {
			if !first {
				w.Write([]byte(","))
			}
			first = false
			w.Write([]byte("\"" + k + "\":"))
			writeValue(w, v)
		}
		w.Write([]byte("}"))
	}
}

func writeValue(w http.ResponseWriter, v interface{}) {
	switch val := v.(type) {
	case float64:
		w.Write([]byte(formatFloat(val)))
	case int64:
		w.Write([]byte(strconv.FormatInt(val, 10)))
	case int:
		w.Write([]byte(strconv.Itoa(val)))
	case string:
		w.Write([]byte("\"" + val + "\""))
	case map[string]int64:
		w.Write([]byte("{"))
		first := true
		for k, v := range val {
			if !first {
				w.Write([]byte(","))
			}
			first = false
			w.Write([]byte("\"" + k + "\":" + strconv.FormatInt(v, 10)))
		}
		w.Write([]byte("}"))
	case map[int]int64:
		w.Write([]byte("{"))
		first := true
		for k, v := range val {
			if !first {
				w.Write([]byte(","))
			}
			first = false
			w.Write([]byte("\"" + strconv.Itoa(k) + "\":" + strconv.FormatInt(v, 10)))
		}
		w.Write([]byte("}"))
	case map[string]string:
		w.Write([]byte("{"))
		first := true
		for k, v := range val {
			if !first {
				w.Write([]byte(","))
			}
			first = false
			w.Write([]byte("\"" + k + "\":\"" + v + "\""))
		}
		w.Write([]byte("}"))
	case map[string]float64:
		w.Write([]byte("{"))
		first := true
		for k, v := range val {
			if !first {
				w.Write([]byte(","))
			}
			first = false
			w.Write([]byte("\"" + k + "\":" + formatFloat(v)))
		}
		w.Write([]byte("}"))
	default:
		w.Write([]byte("null"))
	}
}
