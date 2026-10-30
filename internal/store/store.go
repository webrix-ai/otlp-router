// Package store persists the target list the UI edits.
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
var ErrConflict = errors.New("targets were changed elsewhere; reload and try again")

const pollInterval = 5 * time.Second

type Store interface {
	// Load returns the stored targets and their version. found is false when
	// nothing has been stored yet.
	Load(ctx context.Context) (targets []config.Target, version string, found bool, err error)
	// Save writes targets if the stored version still equals version ("" for
	// a first write) and returns the new version.
	Save(ctx context.Context, targets []config.Target, version string) (string, error)
	Kind() string
}

type document struct {
	Targets []config.Target `yaml:"targets"`
}

func encode(targets []config.Target) ([]byte, error) {
	return yaml.Marshal(document{Targets: targets})
}

func decode(raw []byte) ([]config.Target, error) {
	var doc document
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc.Targets, nil
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
func Watch(ctx context.Context, s Store, since string, log *slog.Logger, onChange func([]config.Target, string)) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	last := since
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		targets, version, found, err := s.Load(ctx)
		if err != nil {
			log.Warn("store poll failed", "store", s.Kind(), "err", err)
			continue
		}
		if !found || version == last {
			continue
		}
		last = version
		onChange(targets, version)
	}
}
