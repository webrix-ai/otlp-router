package admin

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/webrix-ai/otlp-router/internal/exporter"
	"github.com/webrix-ai/otlp-router/internal/router"
)

const (
	peerHeader  = "X-Otlp-Router-Peer"
	peerTimeout = 2 * time.Second
)

// podStats is what one replica counted since it started.
type podStats struct {
	Ingest  router.IngestSnapshot        `json:"ingest"`
	Targets map[string]exporter.Snapshot `json:"targets"`
}

// podsReport says how many replicas the merged stats cover.
type podsReport struct {
	Reporting int    `json:"reporting"`
	Total     int    `json:"total"`
	Error     string `json:"error,omitempty"`
}

func (s *server) localStats() podStats {
	return podStats{Ingest: s.Router.IngestStats(), Targets: s.Router.TargetStats()}
}

func (s *server) peerToken() string { return s.auth.sign("peer-stats") }

func (s *server) isPeer(r *http.Request) bool {
	return s.Config.Peers != "" && hmac.Equal([]byte(r.Header.Get(peerHeader)), []byte(s.peerToken()))
}

// internalStats serves this replica's own counters to the other replicas.
func (s *server) internalStats(w http.ResponseWriter, r *http.Request) {
	if !s.isPeer(r) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, s.localStats())
}

func (s *server) lookupPeers(ctx context.Context) ([]string, error) {
	host, port, _ := net.SplitHostPort(s.Config.Peers)
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	addrs := make([]string, len(ips))
	for i, ip := range ips {
		addrs[i] = net.JoinHostPort(ip, port)
	}
	return addrs, nil
}

// clusterStats adds up the counters of every replica, this one included.
// Replicas that do not answer are left out and counted in the report.
func (s *server) clusterStats(ctx context.Context) (podStats, podsReport) {
	got, pods := fanOut[podStats](ctx, s, "/internal/stats")
	if len(got) == 0 {
		return s.localStats(), pods
	}
	return mergeStats(got), pods
}

// fanOut asks every replica, this one included, for path. When none answers
// it returns nothing and a report for falling back to this replica.
func fanOut[T any](ctx context.Context, s *server, path string) ([]T, podsReport) {
	ctx, cancel := context.WithTimeout(ctx, peerTimeout)
	defer cancel()
	addrs, err := s.resolvePeers(ctx)
	if err != nil || len(addrs) == 0 {
		if err == nil {
			err = fmt.Errorf("no addresses")
		}
		s.Log.Warn("peer lookup failed, showing this replica only", "peers", s.Config.Peers, "err", err)
		return nil, podsReport{Reporting: 1, Total: 1, Error: "could not find the other replicas: " + err.Error()}
	}

	results := make([]*T, len(addrs))
	var wg sync.WaitGroup
	for i, addr := range addrs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := fetchPeer[T](ctx, s, addr, path)
			if err != nil {
				s.Log.Warn("peer request failed", "addr", addr, "path", path, "err", err)
				return
			}
			results[i] = p
		}()
	}
	wg.Wait()

	var got []T
	for _, p := range results {
		if p != nil {
			got = append(got, *p)
		}
	}
	if len(got) == 0 {
		return nil, podsReport{Reporting: 1, Total: len(addrs), Error: "no replica answered; showing this replica only"}
	}
	return got, podsReport{Reporting: len(got), Total: len(addrs)}
}

func fetchPeer[T any](ctx context.Context, s *server, addr, path string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(peerHeader, s.peerToken())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	var p T
	if err := json.NewDecoder(res.Body).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func mergeStats(pods []podStats) podStats {
	out := podStats{
		Ingest:  router.IngestSnapshot{Items: map[string]int64{}, Batches: map[string]int64{}},
		Targets: map[string]exporter.Snapshot{},
	}
	agents := map[string]*router.AgentStats{}
	for _, p := range pods {
		for k, v := range p.Ingest.Items {
			out.Ingest.Items[k] += v
		}
		for k, v := range p.Ingest.Batches {
			out.Ingest.Batches[k] += v
		}
		for _, a := range p.Ingest.Agents {
			m := agents[a.Agent]
			if m == nil {
				m = &router.AgentStats{Agent: a.Agent, Items: map[string]int64{}}
				agents[a.Agent] = m
			}
			for k, v := range a.Items {
				m.Items[k] += v
			}
			m.LastSeenAt = max(m.LastSeenAt, a.LastSeenAt)
		}
		for id, t := range p.Targets {
			out.Targets[id] = mergeTarget(out.Targets[id], t)
		}
	}
	for _, a := range agents {
		out.Ingest.Agents = append(out.Ingest.Agents, *a)
	}
	sort.Slice(out.Ingest.Agents, func(i, j int) bool {
		return out.Ingest.Agents[i].LastSeenAt > out.Ingest.Agents[j].LastSeenAt
	})
	return out
}

func mergeTarget(a, b exporter.Snapshot) exporter.Snapshot {
	m := exporter.Snapshot{
		Sent:          a.Sent + b.Sent,
		Failed:        a.Failed + b.Failed,
		Dropped:       a.Dropped + b.Dropped,
		Filtered:      a.Filtered + b.Filtered,
		Batches:       a.Batches + b.Batches,
		Retries:       a.Retries + b.Retries,
		QueueLen:      a.QueueLen + b.QueueLen,
		QueueCap:      a.QueueCap + b.QueueCap,
		LastSuccessAt: max(a.LastSuccessAt, b.LastSuccessAt),
		LastError:     a.LastError,
		LastErrorAt:   a.LastErrorAt,
	}
	if b.LastErrorAt > a.LastErrorAt {
		m.LastError, m.LastErrorAt = b.LastError, b.LastErrorAt
	}
	return m
}
