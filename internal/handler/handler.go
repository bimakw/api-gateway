package handler

import (
	"encoding/json"
	"net/http"

	"github.com/bimakw/api-gateway/config"
	"github.com/bimakw/api-gateway/internal/apikey"
	"github.com/bimakw/api-gateway/internal/health"
	"github.com/bimakw/api-gateway/internal/proxy"
)

type Handler struct {
	config        *config.Config
	apiKeyMgr     *apikey.Manager
	healthChecker *health.Checker
	reverseProxy  *proxy.ReverseProxy
}

func New(cfg *config.Config, apiKeyMgr *apikey.Manager, healthChecker *health.Checker, rp *proxy.ReverseProxy) *Handler {
	return &Handler{
		config:        cfg,
		apiKeyMgr:     apiKeyMgr,
		healthChecker: healthChecker,
		reverseProxy:  rp,
	}
}

type HealthResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type ServiceInfo struct {
	Name         string `json:"name"`
	PathPrefix   string `json:"path_prefix"`
	TargetURL    string `json:"target_url"`
	Status       string `json:"status,omitempty"`
	ResponseTime int64  `json:"response_time_ms,omitempty"`
}

type InfoResponse struct {
	Status   string        `json:"status"`
	Version  string        `json:"version"`
	Services []ServiceInfo `json:"services"`
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:  "ok",
		Message: "API Gateway is running",
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	services := make([]ServiceInfo, len(h.config.Services))
	for i, svc := range h.config.Services {
		svcInfo := ServiceInfo{
			Name:       svc.Name,
			PathPrefix: svc.PathPrefix,
			TargetURL:  svc.TargetURL,
		}

		// Add health status if checker is available
		if h.healthChecker != nil {
			if health := h.healthChecker.GetHealth(svc.Name); health != nil {
				svcInfo.Status = string(health.Status)
				svcInfo.ResponseTime = health.ResponseTime
			}
		}

		services[i] = svcInfo
	}

	resp := InfoResponse{
		Status:   "ok",
		Version:  "1.0.0",
		Services: services,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ServicesHealth(w http.ResponseWriter, r *http.Request) {
	if h.healthChecker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "Health checker not available",
			"message": "Service health checking is not enabled",
		})
		return
	}

	healthStatuses := h.healthChecker.GetAllHealth()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"services": healthStatuses,
	})
}

func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req apikey.CreateKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "Invalid request",
			"message": err.Error(),
		})
		return
	}

	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "Invalid request",
			"message": "name is required",
		})
		return
	}

	result, err := h.apiKeyMgr.CreateKey(r.Context(), &req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "Failed to create API key",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status":  "success",
		"message": "API key created. Save the raw_key - it won't be shown again!",
		"data":    result,
	})
}

func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.apiKeyMgr.ListKeys(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "Failed to list API keys",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   keys,
	})
}

// RevokeAPIKey disables an API key
func (h *Handler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "Invalid request",
			"message": "id is required",
		})
		return
	}

	if err := h.apiKeyMgr.RevokeKey(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "Failed to revoke API key",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": "API key revoked",
	})
}

// DeleteAPIKey permanently removes an API key
func (h *Handler) DeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "Invalid request",
			"message": "id is required",
		})
		return
	}

	if err := h.apiKeyMgr.DeleteKey(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "Failed to delete API key",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": "API key deleted",
	})
}

func (h *Handler) GetCircuitBreakers(w http.ResponseWriter, r *http.Request) {
	if h.reverseProxy == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "Reverse proxy not available",
			"message": "Circuit breaker functionality is not enabled",
		})
		return
	}

	stats := h.reverseProxy.GetCircuitBreakerStats()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   stats,
	})
}

func (h *Handler) ResetCircuitBreaker(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "Invalid request",
			"message": "service name is required",
		})
		return
	}

	if h.reverseProxy == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "Reverse proxy not available",
			"message": "Circuit breaker functionality is not enabled",
		})
		return
	}

	if !h.reverseProxy.ResetCircuitBreaker(name) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error":   "Not found",
			"message": "Circuit breaker for service '" + name + "' not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": "Circuit breaker for '" + name + "' has been reset",
	})
}

func (h *Handler) ResetAllCircuitBreakers(w http.ResponseWriter, r *http.Request) {
	if h.reverseProxy == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "Reverse proxy not available",
			"message": "Circuit breaker functionality is not enabled",
		})
		return
	}

	h.reverseProxy.ResetAllCircuitBreakers()
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": "All circuit breakers have been reset",
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
