// Package store persists what the UI edits: the target list and the ingest
// tokens.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/webrix-ai/otlp-router/internal/config"
)

// ErrConflict means another replica or user saved since the caller's version.
var ErrConflict = errors.New("the router's settings were changed elsewhere; reload and try again")

const pollInterval = 5 * time.Second

// State is everything the store holds, saved and versioned as one document.
type State struct {
	Targets      []config.Target      `yaml:"targets"`
	IngestTokens []config.IngestToken `yaml:"ingestTokens,omitempty"`
}

type Store interface {
	// Load returns the stored state and its version. found is false when
	// nothing has been stored yet.
	Load(ctx context.Context) (state State, version string, found bool, err error)
	// Save writes state if the stored version still equals version ("" for
	// a first write) and returns the new version.
	Save(ctx context.Context, state State, version string) (string, error)
	Kind() string
}

func encode(s State) ([]byte, error) {
	return yaml.Marshal(s)
}

func decode(raw []byte) (State, error) {
	var s State
	err := yaml.Unmarshal(raw, &s)
	return s, err
}

func New(cfg config.Store) (Store, error) {
	switch cfg.Type {
	case config.StoreFile:
		return &File{Path: cfg.Path}, nil
	case config.StoreConfigMap:
		return NewConfigMap(cfg.ConfigMap.Name, cfg.ConfigMap.Namespace)
	case config.StoreMemory, "":
		return &Memory{}, nil
	}
	return nil, fmt.Errorf("unknown store type %q", cfg.Type)
}

// Watch polls s and calls onChange whenever the stored version changes, so
// edits made through another replica are picked up.
func Watch(ctx context.Context, s Store, since string, log *slog.Logger, onChange func(State, string)) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	last := since
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		state, version, found, err := s.Load(ctx)
		if err != nil {
			log.Warn("store poll failed", "store", s.Kind(), "err", err)
			continue
		}
		if !found || version == last {
			continue
		}
		last = version
		onChange(state, version)
	}
}
