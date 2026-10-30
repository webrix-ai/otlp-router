// Package exporter delivers payloads to one target through its own bounded
// queue, so a slow or failing target never holds up the others.
package exporter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/filter"
	"github.com/webrix-ai/otlp-router/internal/telemetry"
)

const (
	defaultQueueSize = 1000
	workers          = 2
	attemptTimeout   = 10 * time.Second
	maxRetryElapsed  = 2 * time.Minute
	initialBackoff   = 500 * time.Millisecond
	maxBackoff       = 30 * time.Second
)

type sender interface {
	send(ctx context.Context, p telemetry.Payload) error
	close() error
}

// permanentError is a rejection retrying cannot fix, e.g. a 400 or 401.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

type Exporter struct {
	Target config.Target
	chain  *filter.Chain
	sender sender
	queue  chan telemetry.Payload
	stats  stats
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	log    *slog.Logger
}

func New(t config.Target, log *slog.Logger) (*Exporter, error) {
	chain, err := compileFilters(t)
	if err != nil {
		return nil, err
	}
	var s sender
	switch t.Type {
	case config.TypeOTLPGRPC:
		s, err = newGRPCSender(t)
	default:
		s, err = newHTTPSender(t)
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Exporter{
		Target: t,
		chain:  chain,
		sender: s,
		queue:  make(chan telemetry.Payload, defaultQueueSize),
		ctx:    ctx,
		cancel: cancel,
		log:    log.With("target", t.ID),
	}
	for i := 0; i < workers; i++ {
		e.wg.Add(1)
		go e.work()
	}
	return e, nil
}

// Offer runs the target's pipeline on a copy of p and queues the result. It
// never blocks: when the queue is full the batch is dropped and counted.
func (e *Exporter) Offer(p telemetry.Payload) {
	if !e.Target.Enabled || !e.Target.Signals.Has(string(p.Signal)) {
		return
	}
	c := p.Clone()
	c.KeepAgents(e.Target.Agents)
	e.chain.Apply(c)
	if c.Empty() {
		e.stats.filtered.Add(1)
		return
	}
	select {
	case e.queue <- c:
	default:
		e.stats.dropped.Add(int64(c.Items()))
		e.stats.setError(fmt.Errorf("queue full, dropped %d %s", c.Items(), c.Signal))
	}
}

// Preview applies this target's pipeline to a copy without sending it.
func Preview(t config.Target, p telemetry.Payload) (telemetry.Payload, error) {
	chain, err := compileFilters(t)
	if err != nil {
		return telemetry.Payload{}, err
	}
	c := p.Clone()
	c.KeepAgents(t.Agents)
	chain.Apply(c)
	return c, nil
}

// Test sends an empty export request, which every OTLP endpoint must accept,
// to check reachability and credentials without sending data.
func Test(ctx context.Context, t config.Target, log *slog.Logger) error {
	var s sender
	var err error
	if t.Type == config.TypeOTLPGRPC {
		s, err = newGRPCSender(t)
	} else {
		s, err = newHTTPSender(t)
	}
	if err != nil {
		return err
	}
	defer s.close()
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	return s.send(ctx, telemetry.FromLogs(emptyLogs()))
}

func (e *Exporter) work() {
	defer e.wg.Done()
	for {
		select {
		case <-e.ctx.Done():
			return
		case p, ok := <-e.queue:
			if !ok {
				return
			}
			e.deliver(p)
		}
	}
}

func (e *Exporter) deliver(p telemetry.Payload) {
	start := time.Now()
	backoff := initialBackoff
	for {
		ctx, cancel := context.WithTimeout(e.ctx, attemptTimeout)
		err := e.sender.send(ctx, p)
		cancel()
		if err == nil {
			e.stats.sent.Add(int64(p.Items()))
			e.stats.batches.Add(1)
			e.stats.lastSuccess.Store(time.Now().UnixMilli())
			return
		}
		var perm permanentError
		if errors.As(err, &perm) || time.Since(start)+backoff > maxRetryElapsed || e.ctx.Err() != nil {
			e.stats.failed.Add(int64(p.Items()))
			e.stats.setError(err)
			e.log.Warn("export failed", "signal", p.Signal, "items", p.Items(), "err", err)
			return
		}
		e.stats.retries.Add(1)
		e.stats.setError(fmt.Errorf("retrying: %w", err))
		select {
		case <-time.After(backoff):
		case <-e.ctx.Done():
			e.stats.failed.Add(int64(p.Items()))
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// Close lets queued batches drain for up to grace, then stops.
func (e *Exporter) Close(grace time.Duration) {
	deadline := time.Now().Add(grace)
	for len(e.queue) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	e.cancel()
	e.wg.Wait()
	_ = e.sender.close()
}

func (e *Exporter) Stats() Snapshot {
	return e.stats.snapshot(len(e.queue), cap(e.queue))
}

type stats struct {
	sent, failed, dropped, filtered, batches, retries atomic.Int64
	lastSuccess                                       atomic.Int64
	mu                                                sync.Mutex
	lastError                                         string
	lastErrorAt                                       int64
}

func (s *stats) setError(err error) {
	s.mu.Lock()
	s.lastError = err.Error()
	s.lastErrorAt = time.Now().UnixMilli()
	s.mu.Unlock()
}

type Snapshot struct {
	Sent          int64  `json:"sent"`
	Failed        int64  `json:"failed"`
	Dropped       int64  `json:"dropped"`
	Filtered      int64  `json:"filteredBatches"`
	Batches       int64  `json:"batches"`
	Retries       int64  `json:"retries"`
	QueueLen      int    `json:"queueLen"`
	QueueCap      int    `json:"queueCap"`
	LastSuccessAt int64  `json:"lastSuccessAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	LastErrorAt   int64  `json:"lastErrorAt,omitempty"`
}

func (s *stats) snapshot(qlen, qcap int) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Sent: s.sent.Load(), Failed: s.failed.Load(), Dropped: s.dropped.Load(),
		Filtered: s.filtered.Load(), Batches: s.batches.Load(), Retries: s.retries.Load(),
		QueueLen: qlen, QueueCap: qcap, LastSuccessAt: s.lastSuccess.Load(),
		LastError: s.lastError, LastErrorAt: s.lastErrorAt,
	}
}

// Carry moves counters from a replaced exporter so editing a target does not
// reset its numbers in the UI.
func (e *Exporter) Carry(prev *Exporter) {
	if prev == nil {
		return
	}
	p := &prev.stats
	e.stats.sent.Store(p.sent.Load())
	e.stats.failed.Store(p.failed.Load())
	e.stats.dropped.Store(p.dropped.Load())
	e.stats.filtered.Store(p.filtered.Load())
	e.stats.batches.Store(p.batches.Load())
	e.stats.retries.Store(p.retries.Load())
	e.stats.lastSuccess.Store(p.lastSuccess.Load())
	p.mu.Lock()
	e.stats.lastError, e.stats.lastErrorAt = p.lastError, p.lastErrorAt
	p.mu.Unlock()
}

// compileFilters resolves ${env:NAME} salts before compiling the rules.
func compileFilters(t config.Target) (*filter.Chain, error) {
	rules := make([]filter.Rule, len(t.Filters))
	for i, r := range t.Filters {
		r.Salt = config.ExpandEnv(r.Salt)
		rules[i] = r
	}
	return filter.Compile(rules)
}
