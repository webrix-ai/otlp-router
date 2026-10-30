// Package router fans every inbound payload out to the configured targets and
// keeps the live target list in sync with the store.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/exporter"
	"github.com/webrix-ai/otlp-router/internal/store"
	"github.com/webrix-ai/otlp-router/internal/telemetry"
)

const (
	closeGrace    = 5 * time.Second
	sampleCount   = 10
	maxSampleSize = 2000
)

type Router struct {
	store store.Store
	log   *slog.Logger

	mu        sync.RWMutex
	targets   []config.Target
	version   string
	exporters map[string]*exporter.Exporter

	ingest  ingestStats
	samples samples
}

func New(st store.Store, log *slog.Logger) *Router {
	return &Router{
		store:     st,
		log:       log,
		exporters: map[string]*exporter.Exporter{},
		ingest:    ingestStats{agents: map[string]*AgentStats{}},
		samples:   samples{bySignal: map[telemetry.Signal][]telemetry.Payload{}},
	}
}

// Init loads the stored targets, seeding the store from the config file's
// targets on first start, and keeps watching the store for changes.
func (r *Router) Init(ctx context.Context, seed []config.Target) error {
	targets, version, found, err := r.store.Load(ctx)
	if err != nil {
		return err
	}
	if !found {
		if targets, err = config.NormalizeTargets(seed); err != nil {
			return err
		}
		version, err = r.store.Save(ctx, targets, version)
		switch {
		case errors.Is(err, store.ErrConflict):
			// Another replica seeded first; use what it stored.
			if targets, version, _, err = r.store.Load(ctx); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			r.log.Info("seeded targets from config", "store", r.store.Kind(), "targets", len(targets))
		}
	}
	r.apply(targets, version)
	go store.Watch(ctx, r.store, version, r.log, func(t []config.Target, v string) {
		r.log.Info("targets changed in store", "version", v)
		r.apply(t, v)
	})
	return nil
}

// apply swaps in exporters for added or changed targets and retires the rest.
func (r *Router) apply(in []config.Target, version string) {
	targets, err := config.NormalizeTargets(in)
	if err != nil {
		r.log.Error("ignoring invalid stored targets", "err", err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[string]*exporter.Exporter, len(targets))
	var retired []*exporter.Exporter
	for _, t := range targets {
		prev := r.exporters[t.ID]
		if prev != nil && sameTarget(prev.Target, t) {
			next[t.ID] = prev
			continue
		}
		e, err := exporter.New(t, r.log)
		if err != nil {
			r.log.Error("target not started", "target", t.ID, "err", err)
			continue
		}
		e.Carry(prev)
		next[t.ID] = e
		if prev != nil {
			retired = append(retired, prev)
		}
	}
	for id, e := range r.exporters {
		if _, ok := next[id]; !ok {
			retired = append(retired, e)
		}
	}
	r.exporters, r.targets, r.version = next, targets, version
	for _, e := range retired {
		go e.Close(closeGrace)
	}
}

func sameTarget(a, b config.Target) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// Route records the payload and offers it to every target.
func (r *Router) Route(p telemetry.Payload) {
	r.ingest.record(p)
	r.samples.add(p)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.exporters {
		e.Offer(p)
	}
}

func (r *Router) Targets() ([]config.Target, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]config.Target{}, r.targets...), r.version
}

// Update saves the list produced by mutate, guarded by the caller's version,
// and applies it.
func (r *Router) Update(ctx context.Context, version string, mutate func([]config.Target) ([]config.Target, error)) ([]config.Target, string, error) {
	current, currentVersion := r.Targets()
	if version != "" && version != currentVersion {
		return nil, "", store.ErrConflict
	}
	next, err := mutate(current)
	if err != nil {
		return nil, "", err
	}
	if next, err = config.NormalizeTargets(next); err != nil {
		return nil, "", err
	}
	newVersion, err := r.store.Save(ctx, next, currentVersion)
	if err != nil {
		return nil, "", err
	}
	r.apply(next, newVersion)
	return next, newVersion, nil
}

func (r *Router) TargetStats() map[string]exporter.Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]exporter.Snapshot, len(r.exporters))
	for id, e := range r.exporters {
		out[id] = e.Stats()
	}
	return out
}

func (r *Router) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range r.exporters {
		wg.Add(1)
		go func() { defer wg.Done(); e.Close(closeGrace) }()
	}
	wg.Wait()
	r.exporters = map[string]*exporter.Exporter{}
}

// Sample returns the most recent payload of a signal, if any.
func (r *Router) Sample(signal telemetry.Signal) (telemetry.Payload, bool) {
	return r.samples.latest(signal)
}

type AgentStats struct {
	Agent      string           `json:"agent"`
	Items      map[string]int64 `json:"items"`
	LastSeenAt int64            `json:"lastSeenAt"`
}

type IngestSnapshot struct {
	Items   map[string]int64 `json:"items"`
	Batches map[string]int64 `json:"batches"`
	Agents  []AgentStats     `json:"agents"`
}

type ingestStats struct {
	mu      sync.Mutex
	items   [3]int64
	batches [3]int64
	agents  map[string]*AgentStats
}

func signalIndex(s telemetry.Signal) int {
	switch s {
	case telemetry.Traces:
		return 0
	case telemetry.Metrics:
		return 1
	}
	return 2
}

func (s *ingestStats) record(p telemetry.Payload) {
	counts := p.AgentCounts()
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	i := signalIndex(p.Signal)
	s.items[i] += int64(p.Items())
	s.batches[i]++
	for agent, n := range counts {
		a := s.agents[agent]
		if a == nil {
			a = &AgentStats{Agent: agent, Items: map[string]int64{}}
			s.agents[agent] = a
		}
		a.Items[string(p.Signal)] += int64(n)
		a.LastSeenAt = now
	}
}

func (r *Router) IngestStats() IngestSnapshot {
	s := &r.ingest
	s.mu.Lock()
	defer s.mu.Unlock()
	out := IngestSnapshot{Items: map[string]int64{}, Batches: map[string]int64{}}
	for _, sig := range telemetry.AllSignals {
		out.Items[string(sig)] = s.items[signalIndex(sig)]
		out.Batches[string(sig)] = s.batches[signalIndex(sig)]
	}
	for _, a := range s.agents {
		items := make(map[string]int64, len(a.Items))
		for k, v := range a.Items {
			items[k] = v
		}
		out.Agents = append(out.Agents, AgentStats{Agent: a.Agent, Items: items, LastSeenAt: a.LastSeenAt})
	}
	sort.Slice(out.Agents, func(i, j int) bool { return out.Agents[i].LastSeenAt > out.Agents[j].LastSeenAt })
	return out
}

// samples keeps recent payloads in memory for the filter preview. They are
// only shown to the logged-in admin and never persisted.
type samples struct {
	mu       sync.Mutex
	bySignal map[telemetry.Signal][]telemetry.Payload
}

func (s *samples) add(p telemetry.Payload) {
	if p.Items() > maxSampleSize || p.Empty() {
		return
	}
	c := p.Clone()
	s.mu.Lock()
	defer s.mu.Unlock()
	list := append(s.bySignal[p.Signal], c)
	if len(list) > sampleCount {
		list = list[len(list)-sampleCount:]
	}
	s.bySignal[p.Signal] = list
}

func (s *samples) latest(signal telemetry.Signal) (telemetry.Payload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.bySignal[signal]
	if len(list) == 0 {
		return telemetry.Payload{}, false
	}
	return list[len(list)-1], true
}
