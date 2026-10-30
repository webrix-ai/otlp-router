package router

import (
	"context"
	"crypto/subtle"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/store"
)

const (
	maxIngestTokens = 100
	// MaxRotationGrace bounds how long a rotated-out token keeps working.
	MaxRotationGrace = 30 * 24 * time.Hour
	saveAttempts     = 3
)

var ErrTokenNotFound = errors.New("token not found")

// ingestAuth holds the tokens agents may send. Tokens from the config file
// are compared as is; tokens created in the UI are matched by hash.
type ingestAuth struct {
	allowOpen bool
	static    []string

	mu       sync.RWMutex
	tokens   []config.IngestToken
	byHash   map[string]config.IngestToken
	lastUsed sync.Map // token id -> *atomic.Int64, unix ms on this replica
	rejected atomic.Int64
}

func (a *ingestAuth) set(tokens []config.IngestToken) {
	byHash := make(map[string]config.IngestToken, len(tokens))
	for _, t := range tokens {
		byHash[t.Hash] = t
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens, a.byHash = slices.Clone(tokens), byHash
}

func (a *ingestAuth) list() []config.IngestToken {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return slices.Clone(a.tokens)
}

func (a *ingestAuth) authorize(token string, now time.Time) bool {
	if a.allowOpen {
		return true
	}
	if token != "" {
		for _, s := range a.static {
			if subtle.ConstantTimeCompare([]byte(token), []byte(s)) == 1 {
				return true
			}
		}
		a.mu.RLock()
		t, ok := a.byHash[config.HashIngestToken(token)]
		a.mu.RUnlock()
		if ok && !t.Expired(now) {
			v, _ := a.lastUsed.LoadOrStore(t.ID, &atomic.Int64{})
			v.(*atomic.Int64).Store(now.UnixMilli())
			return true
		}
	}
	a.rejected.Add(1)
	return false
}

// SetIngestAuth sets the operator-owned ingest auth from the config file. Call
// it before serving.
func (r *Router) SetIngestAuth(static []string, allowOpen bool) {
	r.auth.static, r.auth.allowOpen = slices.Clone(static), allowOpen
}

// AuthorizeIngest reports whether an agent may send with this bearer token.
func (r *Router) AuthorizeIngest(token string) bool {
	return r.auth.authorize(token, time.Now())
}

type IngestAuthInfo struct {
	Open         bool                 `json:"open"`
	ConfigTokens int                  `json:"configTokens"`
	Tokens       []config.IngestToken `json:"tokens"`
	LastUsed     map[string]int64     `json:"lastUsed"`
	Rejected     int64                `json:"rejected"`
	Version      string               `json:"version"`
}

func (r *Router) IngestAuth() IngestAuthInfo {
	state, version := r.state()
	info := IngestAuthInfo{
		Open:         r.auth.allowOpen,
		ConfigTokens: len(r.auth.static),
		Tokens:       state.IngestTokens,
		LastUsed:     map[string]int64{},
		Rejected:     r.auth.rejected.Load(),
		Version:      version,
	}
	if info.Tokens == nil {
		info.Tokens = []config.IngestToken{}
	}
	r.auth.lastUsed.Range(func(k, v any) bool {
		info.LastUsed[k.(string)] = v.(*atomic.Int64).Load()
		return true
	})
	return info
}

// CreateIngestToken stores a new token and returns it. This is the only time
// the token itself is available.
func (r *Router) CreateIngestToken(ctx context.Context, name string) (string, config.IngestToken, error) {
	secret, tok, err := config.NewIngestToken(name, time.Now())
	if err != nil {
		return "", config.IngestToken{}, err
	}
	err = r.updateTokens(ctx, func(ts []config.IngestToken, _ time.Time) ([]config.IngestToken, error) {
		if len(ts) >= maxIngestTokens {
			return nil, errors.New("too many tokens; revoke unused ones first")
		}
		return append(ts, tok), nil
	})
	return secret, tok, err
}

// RotateIngestToken issues a replacement with the same name. The old token
// keeps working for grace, so agents can be moved over, or stops now if grace
// is zero.
func (r *Router) RotateIngestToken(ctx context.Context, id string, grace time.Duration) (string, config.IngestToken, error) {
	if grace < 0 || grace > MaxRotationGrace {
		return "", config.IngestToken{}, errors.New("grace period must be between 0 and 30 days")
	}
	var secret string
	var tok config.IngestToken
	err := r.updateTokens(ctx, func(ts []config.IngestToken, now time.Time) ([]config.IngestToken, error) {
		i := slices.IndexFunc(ts, func(t config.IngestToken) bool { return t.ID == id })
		if i < 0 {
			return nil, ErrTokenNotFound
		}
		var err error
		if secret, tok, err = config.NewIngestToken(ts[i].Name, now); err != nil {
			return nil, err
		}
		if grace == 0 {
			ts = slices.Delete(ts, i, i+1)
		} else {
			until := now.Add(grace).UTC().Truncate(time.Second)
			if ts[i].ExpiresAt == nil || until.Before(*ts[i].ExpiresAt) {
				ts[i].ExpiresAt = &until
			}
		}
		return append(ts, tok), nil
	})
	return secret, tok, err
}

func (r *Router) RevokeIngestToken(ctx context.Context, id string) error {
	return r.updateTokens(ctx, func(ts []config.IngestToken, _ time.Time) ([]config.IngestToken, error) {
		i := slices.IndexFunc(ts, func(t config.IngestToken) bool { return t.ID == id })
		if i < 0 {
			return nil, ErrTokenNotFound
		}
		return slices.Delete(ts, i, i+1), nil
	})
}

// updateTokens saves the tokens produced by mutate, dropping expired ones. It
// does not take a caller version: token changes don't depend on what the UI
// last saw, so a conflicting save is retried on the latest state.
func (r *Router) updateTokens(ctx context.Context, mutate func([]config.IngestToken, time.Time) ([]config.IngestToken, error)) error {
	for attempt := 0; ; attempt++ {
		state, version := r.state()
		now := time.Now()
		live := slices.DeleteFunc(state.IngestTokens, func(t config.IngestToken) bool { return t.Expired(now) })
		next, err := mutate(live, now)
		if err != nil {
			return err
		}
		state.IngestTokens = next
		newVersion, err := r.store.Save(ctx, state, version)
		if errors.Is(err, store.ErrConflict) && attempt+1 < saveAttempts {
			if err := r.reload(ctx); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		r.apply(state, newVersion)
		return nil
	}
}
