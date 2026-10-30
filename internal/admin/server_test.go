package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/router"
	"github.com/webrix-ai/otlp-router/internal/store"
)

type client struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
	rt     *router.Router
}

func (c *client) do(method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == sessionCookie {
			c.cookie = ck
		}
	}
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func setup(t *testing.T, readOnly bool) *client {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := &store.Memory{}
	rt := router.New(st, log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(rt.Close)
	err := rt.Init(ctx, []config.Target{{
		Name: "Vendor", Enabled: true, Type: config.TypeOTLPHTTP, Endpoint: "http://127.0.0.1:1",
		Headers: map[string]string{"api-key": "literal-secret", "x-ref": "${env:VENDOR_KEY}"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ReadOnly: readOnly, Auth: config.Auth{Admin: config.AdminUser{Username: "admin", Password: "pw"}}}
	h, err := Handler(Options{
		Config: cfg, Router: rt, Store: st, Log: log, Version: "test",
		UI: fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, h: h, rt: rt}
}

func TestLoginRequired(t *testing.T) {
	c := setup(t, false)
	if rec, _ := c.do("GET", "/api/targets", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if rec, _ := c.do("POST", "/api/login", map[string]string{"username": "admin", "password": "nope"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password: want 401, got %d", rec.Code)
	}
	if rec, _ := c.do("POST", "/api/login", map[string]string{"username": "admin", "password": "pw"}); rec.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d", rec.Code)
	}
	if rec, _ := c.do("GET", "/api/targets", nil); rec.Code != http.StatusOK {
		t.Fatalf("after login: want 200, got %d", rec.Code)
	}
}

func TestSecretsMaskedAndPreserved(t *testing.T) {
	c := setup(t, false)
	c.do("POST", "/api/login", map[string]string{"username": "admin", "password": "pw"})
	rec, body := c.do("GET", "/api/targets", nil)
	if strings.Contains(rec.Body.String(), "literal-secret") {
		t.Fatal("literal secret leaked in API response")
	}
	if !strings.Contains(rec.Body.String(), "${env:VENDOR_KEY}") {
		t.Fatal("env references should be shown as is")
	}

	target := body["targets"].([]any)[0].(map[string]any)
	target["enabled"] = false
	rec, _ = c.do("PUT", "/api/targets/vendor", map[string]any{"target": target, "version": body["version"]})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	stored, _ := c.rt.Targets()
	if stored[0].Enabled || stored[0].Headers["api-key"] != "literal-secret" {
		t.Fatalf("masked value overwrote the stored secret: %+v", stored[0])
	}
}

func TestStaleVersionConflicts(t *testing.T) {
	c := setup(t, false)
	c.do("POST", "/api/login", map[string]string{"username": "admin", "password": "pw"})
	if rec, _ := c.do("POST", "/api/targets/vendor/enabled", map[string]any{"enabled": false, "version": "stale"}); rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
}

func TestReadOnlyAndJSONOnly(t *testing.T) {
	c := setup(t, true)
	c.do("POST", "/api/login", map[string]string{"username": "admin", "password": "pw"})
	if rec, _ := c.do("POST", "/api/targets/vendor/enabled", map[string]any{"enabled": false}); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only: want 403, got %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/api/targets/vendor/enabled", strings.NewReader("enabled=false"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c.cookie)
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: want 415, got %d", rec.Code)
	}
}

func TestSPAFallback(t *testing.T) {
	c := setup(t, false)
	rec, _ := c.do("GET", "/targets", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ui") {
		t.Fatalf("SPA fallback: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := c.do("GET", "/api/nope", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown api route: want 404, got %d", rec.Code)
	}
}
