package router_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/filter"
	"github.com/webrix-ai/otlp-router/internal/ingest"
	"github.com/webrix-ai/otlp-router/internal/router"
	"github.com/webrix-ai/otlp-router/internal/store"
	"github.com/webrix-ai/otlp-router/internal/telemetry"
)

type received struct {
	path, contentType, auth, deviceID string
	body                              []byte
}

type sink struct {
	mu   sync.Mutex
	got  []received
	srv  *httptest.Server
	code int
}

func newSink(t *testing.T, code int) *sink {
	s := &sink{code: code}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.got = append(s.got, received{r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Authorization"), r.Header.Get("X-Device-Id"), b})
		s.mu.Unlock()
		w.WriteHeader(s.code)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sink) wait(t *testing.T, n int) []received {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		if len(s.got) >= n {
			out := append([]received{}, s.got...)
			s.mu.Unlock()
			return out
		}
		s.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d requests", n)
	return nil
}

func claudeLogsProto(t *testing.T) []byte {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "claude-code")
	lrs := rl.ScopeLogs().AppendEmpty().LogRecords()
	p := lrs.AppendEmpty()
	p.Body().SetStr("claude_code.user_prompt")
	p.Attributes().PutStr("event.name", "user_prompt")
	p.Attributes().PutStr("user.email", "dev@acme.com")
	a := lrs.AppendEmpty()
	a.Body().SetStr("claude_code.api_request")
	a.Attributes().PutStr("user.email", "dev@acme.com")
	b, err := (&plog.ProtoMarshaler{}).MarshalLogs(ld)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFanOutWithPerTargetFilters(t *testing.T) {
	vendor := newSink(t, http.StatusOK)
	willow := newSink(t, http.StatusOK)
	codexOnly := newSink(t, http.StatusOK)

	rt := router.New(&store.Memory{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(rt.Close)
	t.Setenv("WILLOW_TOKEN", "acme:sig")
	err := rt.Init(ctx, []config.Target{
		{Name: "Vendor", Enabled: true, Type: config.TypeOTLPHTTP, Endpoint: vendor.srv.URL},
		{
			Name: "Willow", Enabled: true, Type: config.TypeWillow,
			Willow:  &config.WillowSettings{Org: "acme", Token: "${env:WILLOW_TOKEN}", BaseURL: willow.srv.URL + "/otlp"},
			Filters: []filter.Rule{{Type: filter.DropLogEvents, Names: []string{"*user_prompt"}}, {Type: filter.DropAttributes, Keys: []string{"user.email"}}},
		},
		{Name: "Codex only", Enabled: true, Type: config.TypeOTLPHTTP, Endpoint: codexOnly.srv.URL, Agents: []string{"codex"}},
		{Name: "Off", Enabled: false, Type: config.TypeOTLPHTTP, Endpoint: codexOnly.srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}

	rt.SetIngestAuth([]string{"ingest-secret"}, false)
	in := &ingest.Server{Router: rt, Authorize: rt.AuthorizeIngest}
	h := in.HTTPHandler()

	req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(claudeLogsProto(t)))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: want 401, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(claudeLogsProto(t)))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Authorization", "Bearer ingest-secret")
	req.Header.Set("X-Device-Id", "dev-123")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest: want 200, got %d: %s", rec.Code, rec.Body)
	}

	v := vendor.wait(t, 1)[0]
	if v.path != "/v1/logs" || v.contentType != "application/x-protobuf" {
		t.Fatalf("vendor got %s %s", v.path, v.contentType)
	}
	vp, _ := telemetry.Decode(telemetry.Logs, telemetry.Proto, v.body)
	if vp.Items() != 2 {
		t.Fatalf("vendor should get both records unfiltered, got %d", vp.Items())
	}

	w := willow.wait(t, 1)[0]
	if w.path != "/otlp/v1/logs" || w.contentType != "application/json" || w.auth != "Bearer acme:sig" || w.deviceID != "dev-123" {
		t.Fatalf("willow request: %+v", w)
	}
	if strings.Contains(string(w.body), "user_prompt") || strings.Contains(string(w.body), "dev@acme.com") {
		t.Fatalf("willow payload not filtered: %s", w.body)
	}

	time.Sleep(200 * time.Millisecond)
	codexOnly.mu.Lock()
	n := len(codexOnly.got)
	codexOnly.mu.Unlock()
	if n != 0 {
		t.Fatalf("codex-only and disabled targets should get nothing, got %d requests", n)
	}

	ing := rt.IngestStats()
	if ing.Items["logs"] != 2 || len(ing.Agents) != 1 || ing.Agents[0].Agent != "claude-code" {
		t.Fatalf("ingest stats: %+v", ing)
	}
}

func TestPermanentFailureIsNotRetried(t *testing.T) {
	bad := newSink(t, http.StatusBadRequest)
	rt := router.New(&store.Memory{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(rt.Close)
	if err := rt.Init(ctx, []config.Target{{Name: "Bad", Enabled: true, Type: config.TypeOTLPHTTP, Endpoint: bad.srv.URL, Encoding: "json"}}); err != nil {
		t.Fatal(err)
	}
	p, _ := telemetry.Decode(telemetry.Logs, telemetry.Proto, claudeLogsProto(t))
	rt.Route(p)
	bad.wait(t, 1)
	time.Sleep(700 * time.Millisecond)
	st := rt.TargetStats()["bad"]
	if st.Retries != 0 || st.Failed != 2 || !strings.Contains(st.LastError, "HTTP 400") {
		t.Fatalf("stats: %+v", st)
	}
}

func TestUpdateKeepsStatsAndDetectsConflicts(t *testing.T) {
	sinkA := newSink(t, http.StatusOK)
	rt := router.New(&store.Memory{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(rt.Close)
	if err := rt.Init(ctx, []config.Target{{Name: "A", Enabled: true, Type: config.TypeOTLPHTTP, Endpoint: sinkA.srv.URL}}); err != nil {
		t.Fatal(err)
	}
	p, _ := telemetry.Decode(telemetry.Logs, telemetry.Proto, claudeLogsProto(t))
	rt.Route(p)
	sinkA.wait(t, 1)
	time.Sleep(50 * time.Millisecond)

	_, version := rt.Targets()
	_, _, err := rt.Update(ctx, version, func(ts []config.Target) ([]config.Target, error) {
		ts[0].Signals = config.Signals{Logs: true}
		return ts, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.TargetStats()["a"].Sent; got != 2 {
		t.Fatalf("stats should survive an edit, sent=%d", got)
	}
	if _, _, err := rt.Update(ctx, version, func(ts []config.Target) ([]config.Target, error) { return ts, nil }); err != store.ErrConflict {
		t.Fatalf("stale version: want ErrConflict, got %v", err)
	}
}
