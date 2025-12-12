package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Manager struct {
	client *redis.Client
}

type APIKey struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	KeyHash     string    `json:"key_hash"`
	RateLimit   int       `json:"rate_limit"` // requests per minute, 0 = use default
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Active      bool      `json:"active"`
}

type CreateKeyRequest struct {
	Name        string     `json:"name"`
	RateLimit   int        `json:"rate_limit,omitempty"`
	Permissions []string   `json:"permissions,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type CreateKeyResponse struct {
	APIKey  *APIKey `json:"api_key"`
	RawKey  string  `json:"raw_key"` // Only returned once on creation
}

func NewManager(client *redis.Client) *Manager {
	return &Manager{client: client}
}

// CreateKey generates a new API key
func (m *Manager) CreateKey(ctx context.Context, req *CreateKeyRequest) (*CreateKeyResponse, error) {
	// Generate random key
	rawKey, err := generateRandomKey(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	// Hash the key for storage
	keyHash := hashKey(rawKey)

	// Generate ID
	id, err := generateRandomKey(8)
	if err != nil {
		return nil, fmt.Errorf("failed to generate id: %w", err)
	}

	apiKey := &APIKey{
		ID:          id,
		Name:        req.Name,
		KeyHash:     keyHash,
		RateLimit:   req.RateLimit,
		Permissions: req.Permissions,
		CreatedAt:   time.Now(),
		ExpiresAt:   req.ExpiresAt,
		Active:      true,
	}

	// Store in Redis
	data, err := json.Marshal(apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal key: %w", err)
	}

	// Store by hash for lookup
	hashKey := fmt.Sprintf("apikey:hash:%s", keyHash)
	if err := m.client.Set(ctx, hashKey, data, 0).Err(); err != nil {
		return nil, fmt.Errorf("failed to store key: %w", err)
	}

	// Store by ID for management
	idKey := fmt.Sprintf("apikey:id:%s", id)
	if err := m.client.Set(ctx, idKey, data, 0).Err(); err != nil {
		return nil, fmt.Errorf("failed to store key by id: %w", err)
	}

	// Add to list of all keys
	if err := m.client.SAdd(ctx, "apikey:list", id).Err(); err != nil {
		return nil, fmt.Errorf("failed to add key to list: %w", err)
	}

	return &CreateKeyResponse{
		APIKey: apiKey,
		RawKey: rawKey,
	}, nil
}

// ValidateKey checks if an API key is valid
func (m *Manager) ValidateKey(ctx context.Context, rawKey string) (*APIKey, error) {
	keyHash := hashKey(rawKey)
	redisKey := fmt.Sprintf("apikey:hash:%s", keyHash)

	data, err := m.client.Get(ctx, redisKey).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("invalid API key")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lookup key: %w", err)
	}

	var apiKey APIKey
	if err := json.Unmarshal(data, &apiKey); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key: %w", err)
	}

	if !apiKey.Active {
		return nil, fmt.Errorf("API key is disabled")
	}

	if apiKey.ExpiresAt != nil && time.Now().After(*apiKey.ExpiresAt) {
		return nil, fmt.Errorf("API key has expired")
	}

	return &apiKey, nil
}

// GetKey retrieves an API key by ID
func (m *Manager) GetKey(ctx context.Context, id string) (*APIKey, error) {
	redisKey := fmt.Sprintf("apikey:id:%s", id)

	data, err := m.client.Get(ctx, redisKey).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("API key not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lookup key: %w", err)
	}

	var apiKey APIKey
	if err := json.Unmarshal(data, &apiKey); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key: %w", err)
	}

	return &apiKey, nil
}

// ListKeys returns all API keys
func (m *Manager) ListKeys(ctx context.Context) ([]*APIKey, error) {
	ids, err := m.client.SMembers(ctx, "apikey:list").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list keys: %w", err)
	}

	keys := make([]*APIKey, 0, len(ids))
	for _, id := range ids {
		key, err := m.GetKey(ctx, id)
		if err == nil {
			// Don't expose the hash
			key.KeyHash = ""
			keys = append(keys, key)
		}
	}

	return keys, nil
}

// RevokeKey disables an API key
func (m *Manager) RevokeKey(ctx context.Context, id string) error {
	apiKey, err := m.GetKey(ctx, id)
	if err != nil {
		return err
	}

	apiKey.Active = false

	data, err := json.Marshal(apiKey)
	if err != nil {
		return fmt.Errorf("failed to marshal key: %w", err)
	}

	// Update both storage locations
	hashKey := fmt.Sprintf("apikey:hash:%s", apiKey.KeyHash)
	idKey := fmt.Sprintf("apikey:id:%s", id)

	pipe := m.client.Pipeline()
	pipe.Set(ctx, hashKey, data, 0)
	pipe.Set(ctx, idKey, data, 0)
	_, err = pipe.Exec(ctx)

	return err
}

// DeleteKey permanently removes an API key
func (m *Manager) DeleteKey(ctx context.Context, id string) error {
	apiKey, err := m.GetKey(ctx, id)
	if err != nil {
		return err
	}

	hashKey := fmt.Sprintf("apikey:hash:%s", apiKey.KeyHash)
	idKey := fmt.Sprintf("apikey:id:%s", id)

	pipe := m.client.Pipeline()
	pipe.Del(ctx, hashKey)
	pipe.Del(ctx, idKey)
	pipe.SRem(ctx, "apikey:list", id)
	_, err = pipe.Exec(ctx)

	return err
}

func generateRandomKey(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func hashKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}
