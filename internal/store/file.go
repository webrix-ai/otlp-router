package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/webrix-ai/otlp-router/internal/config"
)

// File stores targets in a YAML file, versioned by content hash.
type File struct {
	Path string
	mu   sync.Mutex
}

func (f *File) Kind() string { return "file" }

func (f *File) Load(_ context.Context) ([]config.Target, string, bool, error) {
	raw, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	targets, err := decode(raw)
	return targets, hash(raw), true, err
}

func (f *File) Save(ctx context.Context, targets []config.Target, version string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, current, _, err := f.Load(ctx)
	if err != nil {
		return "", err
	}
	if current != version {
		return "", ErrConflict
	}
	raw, err := encode(targets)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return "", err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		return "", err
	}
	return hash(raw), nil
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// Memory keeps targets in process; they reset on restart.
type Memory struct {
	mu      sync.Mutex
	raw     []byte
	version string
}

func (m *Memory) Kind() string { return "memory" }

func (m *Memory) Load(_ context.Context) ([]config.Target, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.raw == nil {
		return nil, "", false, nil
	}
	targets, err := decode(m.raw)
	return targets, m.version, true, err
}

func (m *Memory) Save(_ context.Context, targets []config.Target, version string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if version != m.version {
		return "", ErrConflict
	}
	raw, err := encode(targets)
	if err != nil {
		return "", err
	}
	m.raw, m.version = raw, hash(raw)
	return m.version, nil
}
