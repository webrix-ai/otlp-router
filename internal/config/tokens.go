package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// IngestTokenPrefix marks tokens issued by the router, so a leaked one is
// easy to recognize in logs and secret scanners.
const IngestTokenPrefix = "otr_"

// IngestToken is a token created in the UI. Only its SHA-256 hash is stored;
// the token itself is shown once, when it is created.
type IngestToken struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
	Hash string `yaml:"hash" json:"-"`
	// Hint is the token's first characters, enough to tell tokens apart.
	Hint      string     `yaml:"hint" json:"hint"`
	CreatedAt time.Time  `yaml:"createdAt" json:"createdAt"`
	ExpiresAt *time.Time `yaml:"expiresAt,omitempty" json:"expiresAt,omitempty"`
}

func (t IngestToken) Expired(now time.Time) bool {
	return t.ExpiresAt != nil && !now.Before(*t.ExpiresAt)
}

// NewIngestToken returns a fresh token and the record to store for it.
func NewIngestToken(name string, now time.Time) (string, IngestToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", IngestToken{}, fmt.Errorf("name is required")
	}
	if len(name) > 64 {
		return "", IngestToken{}, fmt.Errorf("name must be at most 64 characters")
	}
	secret := make([]byte, 32)
	id := make([]byte, 6)
	if _, err := rand.Read(secret); err != nil {
		return "", IngestToken{}, err
	}
	if _, err := rand.Read(id); err != nil {
		return "", IngestToken{}, err
	}
	token := IngestTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	return token, IngestToken{
		ID:        hex.EncodeToString(id),
		Name:      name,
		Hash:      HashIngestToken(token),
		Hint:      token[:len(IngestTokenPrefix)+6],
		CreatedAt: now.UTC().Truncate(time.Second),
	}, nil
}

func HashIngestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
