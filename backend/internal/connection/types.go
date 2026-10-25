// Package connection owns host-side model credentials. Public values contain metadata only.
package connection

import (
	"encoding/json"
	"time"
)

type Connection struct {
	ID         string     `json:"id" bson:"_id"`
	TenantID   string     `json:"-" bson:"tenantId"`
	OwnerID    string     `json:"ownerId" bson:"ownerId"`
	Name       string     `json:"name" bson:"name"`
	Provider   string     `json:"provider" bson:"provider"`
	BaseURL    string     `json:"baseUrl" bson:"baseUrl"`
	Scope      string     `json:"scope" bson:"scope"`
	Models     []string   `json:"models" bson:"models"`
	Status     string     `json:"status" bson:"status"`
	Generation int        `json:"generation" bson:"generation"`
	Version    int        `json:"version" bson:"version"`
	ExpiresAt  *time.Time `json:"expiresAt" bson:"expiresAt"`
	VerifiedAt *time.Time `json:"verifiedAt" bson:"verifiedAt"`
	Message    string     `json:"message" bson:"message"`
	CreatedAt  time.Time  `json:"createdAt" bson:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt" bson:"updatedAt"`
}
type Input struct {
	Name      string     `json:"name"`
	Provider  string     `json:"provider"`
	BaseURL   string     `json:"baseUrl"`
	Scope     string     `json:"scope"`
	Models    []string   `json:"models"`
	APIKey    string     `json:"apiKey"`
	ExpiresAt *time.Time `json:"expiresAt"`
}
type Mutation struct {
	Version int    `json:"version"`
	APIKey  string `json:"apiKey,omitempty"`
}
type Request struct {
	Action  string          `json:"action"`
	ID      string          `json:"id"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}

// Credential is only returned to trusted Go callers, never serialized or passed into a guest.
type Credential struct {
	Connection
	APIKey string `json:"-"`
}
