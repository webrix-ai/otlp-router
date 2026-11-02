package router

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/withwillowai/otlp-router/internal/exporter"
	"github.com/withwillowai/otlp-router/internal/telemetry"
)

const (
	liveCapacity = 1000
	liveNames    = 20
	// LiveLimit caps the entries one live request returns.
	LiveLimit = 200

	LiveAccepted = "accepted"
	LiveRejected = "rejected"
)

// LiveTarget is what one target did with a request.
type LiveTarget struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Items  int    `json:"items"`
	Error  string `json:"error,omitempty"`
}

// LiveEntry describes one ingest request: who sent it and where it went. It
// never holds the telemetry itself.
type LiveEntry struct {
	ID        string         `json:"id"`
	Time      int64          `json:"time"`
	UpdatedAt int64          `json:"updatedAt"`
	Signal    string         `json:"signal"`
	Status    string         `json:"status"`
	Error     string         `json:"error,omitempty"`
	Items     int            `json:"items"`
	Agents    map[string]int `json:"agents,omitempty"`
	// Names counts the span, metric or log event names in the request.
	Names   map[string]int `json:"names,omitempty"`
	Token   string         `json:"token,omitempty"`
	User    string         `json:"user,omitempty"`
	Device  string         `json:"device,omitempty"`
	Targets []LiveTarget   `json:"targets,omitempty"`
}

type liveRecord struct {
	seq   uint64
	entry LiveEntry
}

// liveLog keeps the last requests of this replica in a ring, in memory only.
type liveLog struct {
	mu   sync.Mutex
	boot string
	seq  uint64
	ring []*liveRecord
}

func newLiveLog() *liveLog {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &liveLog{boot: hex.EncodeToString(b), ring: make([]*liveRecord, liveCapacity)}
}

func (l *liveLog) add(e LiveEntry) uint64 {
	now := time.Now().UnixMilli()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e.ID = fmt.Sprintf("%s-%d", l.boot, l.seq)
	e.Time, e.UpdatedAt = now, now
	l.ring[(l.seq-1)%liveCapacity] = &liveRecord{seq: l.seq, entry: e}
	return l.seq
}

func (l *liveLog) result(seq uint64, r exporter.Result) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec := l.ring[(seq-1)%liveCapacity]
	if rec == nil || rec.seq != seq {
		return
	}
	t := LiveTarget{ID: r.Target, Status: r.Status, Items: r.Items, Error: r.Error}
	e := &rec.entry
	if i := slices.IndexFunc(e.Targets, func(x LiveTarget) bool { return x.ID == t.ID }); i >= 0 {
		e.Targets[i] = t
	} else {
		e.Targets = append(e.Targets, t)
	}
	e.UpdatedAt = max(time.Now().UnixMilli(), e.UpdatedAt)
}

// since returns the newest entries changed after the given unix ms.
func (l *liveLog) since(after int64) []LiveEntry {
	l.mu.Lock()
	out := make([]LiveEntry, 0, LiveLimit)
	for _, rec := range l.ring {
		if rec == nil || rec.entry.UpdatedAt <= after {
			continue
		}
		e := rec.entry
		e.Targets = slices.Clone(e.Targets)
		out = append(out, e)
	}
	l.mu.Unlock()
	return NewestLive(out)
}

// NewestLive sorts entries newest first and keeps LiveLimit of them.
func NewestLive(entries []LiveEntry) []LiveEntry {
	slices.SortFunc(entries, func(a, b LiveEntry) int { return cmp.Compare(b.Time, a.Time) })
	if len(entries) > LiveLimit {
		entries = entries[:LiveLimit]
	}
	return entries
}

func liveEntry(sig telemetry.Signal, headers map[string]string) LiveEntry {
	return LiveEntry{Signal: string(sig), User: headers["x-user-email"], Device: headers["x-device-id"]}
}

// DisableLive turns the live view off. Call it before serving.
func (r *Router) DisableLive() { r.live = nil }

func (r *Router) LiveEnabled() bool { return r.live != nil }

// Live returns this replica's requests changed after the given unix ms.
func (r *Router) Live(after int64) []LiveEntry {
	if r.live == nil {
		return []LiveEntry{}
	}
	return r.live.since(after)
}

// Rejected records a request ingest turned away, e.g. a bad token.
func (r *Router) Rejected(sig telemetry.Signal, reason string, headers map[string]string) {
	if r.live == nil {
		return
	}
	e := liveEntry(sig, headers)
	e.Status, e.Error = LiveRejected, reason
	r.live.add(e)
}

func (r *Router) recordLive(p telemetry.Payload) uint64 {
	if r.live == nil {
		return 0
	}
	e := liveEntry(p.Signal, p.Headers)
	e.Status, e.Items, e.Agents, e.Token = LiveAccepted, p.Items(), p.AgentCounts(), p.TokenName
	e.Names = p.Names(liveNames)
	return r.live.add(e)
}

func (r *Router) reportLive(seq uint64, res exporter.Result) {
	if r.live != nil {
		r.live.result(seq, res)
	}
}
