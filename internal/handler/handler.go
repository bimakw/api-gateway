package handler

import (
	"encoding/json"
	"net/http"

	"github.com/bimakw/api-gateway/config"
	"github.com/bimakw/api-gateway/internal/apikey"
)

type Handler struct {
	config     *config.Config
	apiKeyMgr  *apikey.Manager
}

func New(cfg *config.Config, apiKeyMgr *apikey.Manager) *Handler {
	return &Handler{
		config:    cfg,
		apiKeyMgr: apiKeyMgr,
	}
}

type HealthResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type ServiceInfo struct {
	Name       string `json:"name"`
	PathPrefix string `json:"path_prefix"`
	TargetURL  string `json:"target_url"`
}

type InfoResponse struct {
	Status   string        `json:"status"`
	Version  string        `json:"version"`
	Services []ServiceInfo `json:"services"`
}

// Health returns service health status
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:  "ok",
		Message: "API Gateway is running",
	}
	writeJSON(w, http.StatusOK, resp)
}

// Info returns gateway information
func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	services := make([]ServiceInfo, len(h.config.Services))
	for i, svc := range h.config.Services {
		services[i] = ServiceInfo{
			Name:       svc.Name,
			PathPrefix: svc.PathPrefix,
			TargetURL:  svc.TargetURL,
		}
	}

	resp := InfoResponse{
		Status:   "ok",
		Version:  "1.0.0",
		Services: services,
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateAPIKey creates a new API key
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

// ListAPIKeys returns all API keys
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

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
