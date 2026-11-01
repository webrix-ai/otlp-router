package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/exporter"
	"github.com/webrix-ai/otlp-router/internal/router"
	"github.com/webrix-ai/otlp-router/internal/store"
)

func peerServer(t *testing.T) *server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := &store.Memory{}
	rt := router.New(st, log)
	t.Cleanup(rt.Close)
	if err := rt.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Peers: "otlp-router-peers:8080",
		Auth:  config.Auth{Admin: config.AdminUser{Username: "admin", Password: "pw"}, SessionSecret: "shared"},
	}
	s, err := newServer(Options{Config: cfg, Router: rt, Store: st, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fakePeer serves fixed stats to callers that present the peer token.
func fakePeer(t *testing.T, token string, p podStats) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/stats" || r.Header.Get(peerHeader) != token {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(p)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestInternalStatsNeedsPeerToken(t *testing.T) {
	s := peerServer(t)
	h := s.routes()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/internal/stats", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("without token: want 404, got %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "/internal/stats", nil)
	req.Header.Set(peerHeader, s.peerToken())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with token: want 200, got %d", rec.Code)
	}
}

func TestClusterStatsAddsUpReplicas(t *testing.T) {
	s := peerServer(t)
	a := fakePeer(t, s.peerToken(), podStats{
		Ingest: router.IngestSnapshot{
			Items:   map[string]int64{"logs": 10},
			Batches: map[string]int64{"logs": 2},
			Agents:  []router.AgentStats{{Agent: "codex", Items: map[string]int64{"logs": 10}, LastSeenAt: 100}},
		},
		Targets: map[string]exporter.Snapshot{"willow": {Sent: 64, QueueLen: 1, LastSuccessAt: 500, LastError: "old", LastErrorAt: 50}},
	})
	b := fakePeer(t, s.peerToken(), podStats{
		Ingest: router.IngestSnapshot{
			Items:   map[string]int64{"logs": 5},
			Batches: map[string]int64{"logs": 1},
			Agents:  []router.AgentStats{{Agent: "codex", Items: map[string]int64{"logs": 5}, LastSeenAt: 300}},
		},
		Targets: map[string]exporter.Snapshot{"willow": {Sent: 74, Failed: 370, LastSuccessAt: 400, LastError: "HTTP 400", LastErrorAt: 450}},
	})
	s.resolvePeers = func(context.Context) ([]string, error) { return []string{a, b, "127.0.0.1:1"}, nil }

	got, pods := s.clusterStats(context.Background())
	if pods.Reporting != 2 || pods.Total != 3 {
		t.Fatalf("pods: want 2 of 3, got %+v", pods)
	}
	w := got.Targets["willow"]
	if w.Sent != 138 || w.Failed != 370 || w.QueueLen != 1 || w.LastSuccessAt != 500 || w.LastError != "HTTP 400" {
		t.Fatalf("willow target not merged: %+v", w)
	}
	if got.Ingest.Items["logs"] != 15 || got.Ingest.Batches["logs"] != 3 {
		t.Fatalf("ingest not merged: %+v", got.Ingest)
	}
	if len(got.Ingest.Agents) != 1 || got.Ingest.Agents[0].Items["logs"] != 15 || got.Ingest.Agents[0].LastSeenAt != 300 {
		t.Fatalf("agents not merged: %+v", got.Ingest.Agents)
	}
}

func TestClusterStatsFallsBackToThisReplica(t *testing.T) {
	s := peerServer(t)
	s.resolvePeers = func(context.Context) ([]string, error) { return nil, errors.New("no such host") }
	_, pods := s.clusterStats(context.Background())
	if pods.Reporting != 1 || pods.Total != 1 || pods.Error == "" {
		t.Fatalf("want this replica only with an error, got %+v", pods)
	}
}
