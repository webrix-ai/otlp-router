// Package config loads the router's static configuration and defines the
// target list the UI edits at runtime.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/webrix-ai/otlp-router/internal/filter"
)

// Config is read once at startup. Everything here is owned by the operator
// (Helm values or a mounted file); the UI cannot change it.
type Config struct {
	Listen    Listen   `yaml:"listen"`
	PublicURL string   `yaml:"publicUrl"`
	Auth      Auth     `yaml:"auth"`
	ReadOnly  bool     `yaml:"readOnly"`
	Store     Store    `yaml:"store"`
	Targets   []Target `yaml:"targets"`
}

type Listen struct {
	OTLPGRPC string `yaml:"otlpGrpc"`
	OTLPHTTP string `yaml:"otlpHttp"`
	Admin    string `yaml:"admin"`
}

type Auth struct {
	Admin AdminUser `yaml:"admin"`
	// SessionSecret signs UI session cookies. Must be shared by all replicas.
	SessionSecret string `yaml:"sessionSecret"`
	// IngestTokens are accepted as "Authorization: Bearer <token>" on OTLP
	// ingest. Empty means ingest is open.
	IngestTokens []string `yaml:"ingestTokens"`
}

type AdminUser struct {
	Username string `yaml:"username"`
	// Exactly one of Password or PasswordHash (bcrypt) must be set.
	Password     string `yaml:"password"`
	PasswordHash string `yaml:"passwordHash"`
}

type StoreType string

const (
	StoreMemory    StoreType = "memory"
	StoreFile      StoreType = "file"
	StoreConfigMap StoreType = "configmap"
)

type Store struct {
	Type      StoreType `yaml:"type"`
	Path      string    `yaml:"path"`
	ConfigMap struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"configMap"`
}

type TargetType string

const (
	TypeOTLPHTTP TargetType = "otlphttp"
	TypeOTLPGRPC TargetType = "otlpgrpc"
	TypeWillow   TargetType = "willow"
)

// Target is one destination. Values in Headers, Willow.Token and filter salts
// may be ${env:NAME} references, resolved when the exporter is built, so
// secrets never have to be stored in the target list itself.
type Target struct {
	ID       string     `yaml:"id" json:"id"`
	Name     string     `yaml:"name" json:"name"`
	Enabled  bool       `yaml:"enabled" json:"enabled"`
	Type     TargetType `yaml:"type" json:"type"`
	Endpoint string     `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	// Encoding is proto or json, for otlphttp. Willow always uses json.
	Encoding    string            `yaml:"encoding,omitempty" json:"encoding,omitempty"`
	Compression string            `yaml:"compression,omitempty" json:"compression,omitempty"`
	Insecure    bool              `yaml:"insecure,omitempty" json:"insecure,omitempty"`
	Headers     map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// PassthroughHeaders are copied from the inbound request, e.g. x-device-id.
	PassthroughHeaders []string        `yaml:"passthroughHeaders,omitempty" json:"passthroughHeaders,omitempty"`
	Signals            Signals         `yaml:"signals" json:"signals"`
	Agents             []string        `yaml:"agents,omitempty" json:"agents,omitempty"`
	Filters            []filter.Rule   `yaml:"filters,omitempty" json:"filters,omitempty"`
	Willow             *WillowSettings `yaml:"willow,omitempty" json:"willow,omitempty"`
}

type Signals struct {
	Traces  bool `yaml:"traces" json:"traces"`
	Metrics bool `yaml:"metrics" json:"metrics"`
	Logs    bool `yaml:"logs" json:"logs"`
}

func (s Signals) Has(signal string) bool {
	switch signal {
	case "traces":
		return s.Traces
	case "metrics":
		return s.Metrics
	case "logs":
		return s.Logs
	}
	return false
}

type WillowSettings struct {
	Org string `yaml:"org" json:"org"`
	// Token is the org's OTLP token ("<org>:<signature>").
	Token   string `yaml:"token" json:"token"`
	BaseURL string `yaml:"baseUrl,omitempty" json:"baseUrl,omitempty"`
}

// WillowDefaultPassthrough are the headers Willow uses to attribute telemetry.
var WillowDefaultPassthrough = []string{"x-device-id", "x-user-email", "x-willow-platform"}

func Load(path string) (*Config, error) {
	cfg := &Config{}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	// Only operator-owned fields are expanded here. Target references stay
	// unexpanded so they are persisted as references, not secrets.
	cfg.PublicURL = ExpandEnv(cfg.PublicURL)
	cfg.Auth.Admin.Username = ExpandEnv(cfg.Auth.Admin.Username)
	cfg.Auth.Admin.Password = ExpandEnv(cfg.Auth.Admin.Password)
	cfg.Auth.Admin.PasswordHash = ExpandEnv(cfg.Auth.Admin.PasswordHash)
	cfg.Auth.SessionSecret = ExpandEnv(cfg.Auth.SessionSecret)
	var tokens []string
	for _, t := range cfg.Auth.IngestTokens {
		tokens = append(tokens, strings.Split(ExpandEnv(t), ",")...)
	}
	cfg.Auth.IngestTokens = tokens
	cfg.applyDefaults()
	return cfg, cfg.validate()
}

func (c *Config) applyDefaults() {
	if c.Listen.OTLPGRPC == "" {
		c.Listen.OTLPGRPC = ":4317"
	}
	if c.Listen.OTLPHTTP == "" {
		c.Listen.OTLPHTTP = ":4318"
	}
	if c.Listen.Admin == "" {
		c.Listen.Admin = ":8080"
	}
	if c.Auth.Admin.Username == "" {
		c.Auth.Admin.Username = "admin"
	}
	if c.Store.Type == "" {
		c.Store.Type = StoreMemory
	}
	var tokens []string
	for _, t := range c.Auth.IngestTokens {
		if t = strings.TrimSpace(t); t != "" {
			tokens = append(tokens, t)
		}
	}
	c.Auth.IngestTokens = tokens
}

func (c *Config) validate() error {
	if c.Auth.Admin.Password == "" && c.Auth.Admin.PasswordHash == "" {
		return fmt.Errorf("auth.admin.password or auth.admin.passwordHash is required")
	}
	switch c.Store.Type {
	case StoreMemory:
	case StoreFile:
		if c.Store.Path == "" {
			return fmt.Errorf("store.path is required for the file store")
		}
	case StoreConfigMap:
		if c.Store.ConfigMap.Name == "" {
			return fmt.Errorf("store.configMap.name is required for the configmap store")
		}
	default:
		return fmt.Errorf("unknown store.type %q", c.Store.Type)
	}
	_, err := NormalizeTargets(c.Targets)
	return err
}

var envRef = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnv replaces ${env:NAME} with the environment variable's value.
func ExpandEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(envRef.FindStringSubmatch(m)[1])
	})
}

// IsEnvRef reports whether s is only an ${env:NAME} reference.
func IsEnvRef(s string) bool {
	loc := envRef.FindStringIndex(s)
	return loc != nil && loc[0] == 0 && loc[1] == len(s)
}

var idClean = regexp.MustCompile(`[^a-z0-9-]+`)

// NormalizeTargets fills defaults and validates the list. It returns a copy.
func NormalizeTargets(in []Target) ([]Target, error) {
	out := make([]Target, 0, len(in))
	seen := map[string]bool{}
	for i, t := range in {
		t.Name = strings.TrimSpace(t.Name)
		if t.Name == "" {
			return nil, fmt.Errorf("target %d: name is required", i+1)
		}
		if t.ID == "" {
			t.ID = strings.Trim(idClean.ReplaceAllString(strings.ToLower(t.Name), "-"), "-")
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("target %q: duplicate id %q", t.Name, t.ID)
		}
		seen[t.ID] = true
		if t.Signals == (Signals{}) {
			t.Signals = Signals{Traces: true, Metrics: true, Logs: true}
		}
		switch t.Type {
		case TypeOTLPHTTP:
			if t.Encoding == "" {
				t.Encoding = "proto"
			}
			if t.Encoding != "proto" && t.Encoding != "json" {
				return nil, fmt.Errorf("target %q: encoding must be proto or json", t.Name)
			}
		case TypeOTLPGRPC:
		case TypeWillow:
			if t.Willow == nil || strings.TrimSpace(t.Willow.Org) == "" || t.Willow.Token == "" {
				return nil, fmt.Errorf("target %q: willow.org and willow.token are required", t.Name)
			}
			w := *t.Willow
			w.Org = strings.TrimSpace(w.Org)
			t.Willow = &w
			t.Encoding = "json"
			t.Endpoint = ""
			if t.PassthroughHeaders == nil {
				t.PassthroughHeaders = WillowDefaultPassthrough
			}
		default:
			return nil, fmt.Errorf("target %q: type must be otlphttp, otlpgrpc or willow", t.Name)
		}
		if t.Type != TypeWillow && t.Endpoint == "" {
			return nil, fmt.Errorf("target %q: endpoint is required", t.Name)
		}
		if t.Compression != "" && t.Compression != "gzip" && t.Compression != "none" {
			return nil, fmt.Errorf("target %q: compression must be gzip or none", t.Name)
		}
		if _, err := filter.Compile(t.Filters); err != nil {
			return nil, fmt.Errorf("target %q: %w", t.Name, err)
		}
		out = append(out, t)
	}
	return out, nil
}

// ResolvedEndpoint is where the exporter sends, derived for Willow targets.
func (t Target) ResolvedEndpoint() string {
	if t.Type == TypeWillow && t.Willow != nil {
		return WillowEndpoint(t.Willow)
	}
	return t.Endpoint
}

func WillowEndpoint(w *WillowSettings) string {
	if w.BaseURL != "" {
		return strings.TrimRight(w.BaseURL, "/")
	}
	return fmt.Sprintf("https://%s.withwillow.ai/otlp", w.Org)
}
