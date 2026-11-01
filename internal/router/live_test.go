package router_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/exporter"
	"github.com/webrix-ai/otlp-router/internal/filter"
	"github.com/webrix-ai/otlp-router/internal/ingest"
	"github.com/webrix-ai/otlp-router/internal/router"
	"github.com/webrix-ai/otlp-router/internal/store"
)

func TestLiveShowsWhereEachRequestWent(t *testing.T) {
	ok := newSink(t, http.StatusOK)
	broken := newSink(t, http.StatusBadRequest)

	rt := router.New(&store.Memory{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(rt.Close)
	err := rt.Init(ctx, []config.Target{
		{Name: "Vendor", Enabled: true, Type: config.TypeOTLPHTTP, Endpoint: ok.srv.URL},
		{Name: "Broken", Enabled: true, Type: config.TypeOTLPHTTP, Endpoint: broken.srv.URL},
		{
			Name: "No logs", Enabled: true, Type: config.TypeOTLPHTTP, Endpoint: ok.srv.URL,
			Filters: []filter.Rule{{Type: filter.DropLogEvents, Names: []string{"*"}}},
		},
		{Name: "Off", Enabled: false, Type: config.TypeOTLPHTTP, Endpoint: ok.srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	rt.SetIngestAuth([]string{"ingest-secret"}, false)
	h := (&ingest.Server{Router: rt, Authorize: rt.CheckIngest}).HTTPHandler()

	send := func(auth string) {
		req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(claudeLogsProto(t)))
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("Authorization", auth)
		req.Header.Set("X-User-Email", "dev@acme.com")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	send("Bearer wrong")
	send("Bearer ingest-secret")

	var accepted, rejected router.LiveEntry
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries := rt.Live(0)
		if len(entries) != 2 {
			t.Fatalf("want 2 entries, got %+v", entries)
		}
		for _, e := range entries {
			if e.Status == router.LiveAccepted {
				accepted = e
			} else {
				rejected = e
			}
		}
		if done(accepted) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if rejected.Status != router.LiveRejected || rejected.Error == "" || rejected.User != "dev@acme.com" {
		t.Fatalf("rejected entry: %+v", rejected)
	}
	if accepted.Status != router.LiveAccepted || accepted.Items != 2 || accepted.Agents["claude-code"] != 2 || accepted.Token != router.ConfigTokenName {
		t.Fatalf("accepted entry: %+v", accepted)
	}
	got := map[string]router.LiveTarget{}
	for _, tg := range accepted.Targets {
		got[tg.ID] = tg
	}
	if got["vendor"].Status != exporter.StatusSent || got["vendor"].Items != 2 {
		t.Fatalf("vendor: %+v", got["vendor"])
	}
	if got["broken"].Status != exporter.StatusFailed || got["broken"].Error == "" {
		t.Fatalf("broken: %+v", got["broken"])
	}
	if got["no-logs"].Status != exporter.StatusFiltered {
		t.Fatalf("no-logs: %+v", got["no-logs"])
	}
	if _, listed := got["off"]; listed || len(got) != 3 {
		t.Fatalf("disabled targets are not listed: %+v", accepted.Targets)
	}
	if later := rt.Live(accepted.UpdatedAt); len(later) != 0 {
		t.Fatalf("nothing changed after the last update, got %+v", later)
	}
}

func done(e router.LiveEntry) bool {
	if len(e.Targets) < 3 {
		return false
	}
	for _, tg := range e.Targets {
		if tg.Status == exporter.StatusQueued || tg.Status == exporter.StatusRetrying {
			return false
		}
	}
	return true
}

func TestLiveOff(t *testing.T) {
	rt := router.New(&store.Memory{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rt.DisableLive()
	rt.Rejected("logs", "bad token", nil)
	if rt.LiveEnabled() || len(rt.Live(0)) != 0 {
		t.Fatal("live view should keep nothing when off")
	}
}
