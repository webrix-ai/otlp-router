package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// File stores state in a YAML file, versioned by content hash.
type File struct {
	Path string
	mu   sync.Mutex
}

func (f *File) Kind() string { return "file" }

func (f *File) Load(_ context.Context) (State, string, bool, error) {
	raw, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, "", false, nil
	}
	if err != nil {
		return State{}, "", false, err
	}
	state, err := decode(raw)
	return state, hash(raw), true, err
}

func (f *File) Save(ctx context.Context, state State, version string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, current, _, err := f.Load(ctx)
	if err != nil {
		return "", err
	}
	if current != version {
		return "", ErrConflict
	}
	raw, err := encode(state)
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

// Memory keeps state in process; it resets on restart.
type Memory struct {
	mu      sync.Mutex
	raw     []byte
	version string
}

func (m *Memory) Kind() string { return "memory" }

func (m *Memory) Load(_ context.Context) (State, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.raw == nil {
		return State{}, "", false, nil
	}
	state, err := decode(m.raw)
	return state, m.version, true, err
}

func (m *Memory) Save(_ context.Context, state State, version string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if version != m.version {
		return "", ErrConflict
	}
	raw, err := encode(state)
	if err != nil {
		return "", err
	}
	m.raw, m.version = raw, hash(raw)
	return m.version, nil
}
