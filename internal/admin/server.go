// Package admin serves the router's web UI and its JSON API.
package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/exporter"
	"github.com/webrix-ai/otlp-router/internal/filter"
	"github.com/webrix-ai/otlp-router/internal/router"
	"github.com/webrix-ai/otlp-router/internal/store"
	"github.com/webrix-ai/otlp-router/internal/telemetry"
)

// SecretMask stands in for a stored secret in API responses. Sending it back
// unchanged keeps the stored value.
const SecretMask = "••••••••"

type Options struct {
	Config  *config.Config
	Router  *router.Router
	Store   store.Store
	UI      fs.FS
	Version string
	Log     *slog.Logger
}

type server struct {
	Options
	auth *auth
}

func Handler(o Options) (http.Handler, error) {
	a, err := newAuth(o.Config.Auth)
	if err != nil {
		return nil, err
	}
	s := &server{Options: o, auth: a}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("GET /metrics", s.metrics)

	mux.HandleFunc("GET /api/session", s.getSession)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)

	mux.Handle("GET /api/info", s.authed(s.info))
	mux.Handle("GET /api/stats", s.authed(s.stats))
	mux.Handle("GET /api/targets", s.authed(s.listTargets))
	mux.Handle("POST /api/targets", s.mutating(s.createTarget))
	mux.Handle("PUT /api/targets/{id}", s.mutating(s.updateTarget))
	mux.Handle("DELETE /api/targets/{id}", s.mutating(s.deleteTarget))
	mux.Handle("POST /api/targets/{id}/enabled", s.mutating(s.setEnabled))
	mux.Handle("POST /api/test", s.authed(s.testTarget))
	mux.Handle("POST /api/preview", s.authed(s.preview))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not found") })

	mux.HandleFunc("/", s.static)
	return securityHeaders(mux), nil
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'")
		h.ServeHTTP(w, r)
	})
}

func (s *server) authed(fn http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth.session(r) == "" {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		fn(w, r)
	})
}

// mutating also requires a JSON body, which a cross-site form cannot send
// without a CORS preflight, and refuses changes in read-only mode.
func (s *server) mutating(fn http.HandlerFunc) http.Handler {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "expected application/json")
			return
		}
		if s.Config.ReadOnly {
			writeError(w, http.StatusForbidden, "the router is in read-only mode; change targets in its config")
			return
		}
		fn(w, r)
	})
}

func (s *server) getSession(w http.ResponseWriter, r *http.Request) {
	user := s.auth.session(r)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": user != "", "username": user})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.auth.allowAttempt(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}
	var body struct{ Username, Password string }
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.auth.check(body.Username, body.Password) {
		s.auth.recordFailure(ip)
		s.Log.Warn("failed admin login", "ip", ip)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.auth.issue(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": body.Username})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	s.auth.clear(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

func (s *server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    s.Version,
		"publicUrl":  s.Config.PublicURL,
		"ingestAuth": len(s.Config.Auth.IngestTokens) > 0,
		"readOnly":   s.Config.ReadOnly,
		"store":      s.Store.Kind(),
		"listen":     s.Config.Listen,
		"presets":    filter.Presets,
	})
}

func (s *server) stats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ingest":  s.Router.IngestStats(),
		"targets": s.Router.TargetStats(),
	})
}

func (s *server) listTargets(w http.ResponseWriter, _ *http.Request) {
	targets, version := s.Router.Targets()
	writeJSON(w, http.StatusOK, targetsResponse(targets, version))
}

func targetsResponse(targets []config.Target, version string) map[string]any {
	out := make([]config.Target, len(targets))
	for i, t := range targets {
		out[i] = maskTarget(t)
	}
	return map[string]any{"targets": out, "version": version}
}

type targetRequest struct {
	Target  config.Target `json:"target"`
	Version string        `json:"version"`
}

func (s *server) createTarget(w http.ResponseWriter, r *http.Request) {
	var req targetRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.update(w, r, req.Version, func(ts []config.Target) ([]config.Target, error) {
		if req.Target.ID != "" && slices.ContainsFunc(ts, func(t config.Target) bool { return t.ID == req.Target.ID }) {
			return nil, fmt.Errorf("a target with id %q already exists", req.Target.ID)
		}
		return append(ts, req.Target), nil
	})
}

func (s *server) updateTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req targetRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.update(w, r, req.Version, func(ts []config.Target) ([]config.Target, error) {
		i := slices.IndexFunc(ts, func(t config.Target) bool { return t.ID == id })
		if i < 0 {
			return nil, errNotFound
		}
		next := unmaskTarget(req.Target, ts[i])
		next.ID = id
		ts[i] = next
		return ts, nil
	})
}

func (s *server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct{ Version string }
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.update(w, r, req.Version, func(ts []config.Target) ([]config.Target, error) {
		i := slices.IndexFunc(ts, func(t config.Target) bool { return t.ID == id })
		if i < 0 {
			return nil, errNotFound
		}
		return slices.Delete(ts, i, i+1), nil
	})
}

func (s *server) setEnabled(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Enabled bool   `json:"enabled"`
		Version string `json:"version"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.update(w, r, req.Version, func(ts []config.Target) ([]config.Target, error) {
		i := slices.IndexFunc(ts, func(t config.Target) bool { return t.ID == id })
		if i < 0 {
			return nil, errNotFound
		}
		ts[i].Enabled = req.Enabled
		return ts, nil
	})
}

var errNotFound = errors.New("target not found")

func (s *server) update(w http.ResponseWriter, r *http.Request, version string, mutate func([]config.Target) ([]config.Target, error)) {
	targets, newVersion, err := s.Router.Update(r.Context(), version, mutate)
	switch {
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.Log.Info("targets updated", "version", newVersion)
		writeJSON(w, http.StatusOK, targetsResponse(targets, newVersion))
	}
}

// resolve turns a target from the UI, possibly unsaved and with masked
// secrets, into one usable for testing or previewing.
func (s *server) resolve(t config.Target) (config.Target, error) {
	if t.ID != "" {
		current, _ := s.Router.Targets()
		if i := slices.IndexFunc(current, func(c config.Target) bool { return c.ID == t.ID }); i >= 0 {
			t = unmaskTarget(t, current[i])
		}
	}
	t.Enabled = true
	if t.Name == "" {
		t.Name = "unsaved"
	}
	norm, err := config.NormalizeTargets([]config.Target{t})
	if err != nil {
		return config.Target{}, err
	}
	return norm[0], nil
}

func (s *server) testTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target config.Target `json:"target"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.resolve(req.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := exporter.Test(r.Context(), t, s.Log); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) preview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target config.Target `json:"target"`
		Signal string        `json:"signal"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sig, err := telemetry.ParseSignal(req.Signal)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.resolve(req.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sample, ok := s.Router.Sample(sig)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	after, err := exporter.Preview(t, sample)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	before, _ := sample.Encode(telemetry.JSON)
	afterJSON, _ := after.Encode(telemetry.JSON)
	writeJSON(w, http.StatusOK, map[string]any{
		"available":   true,
		"before":      json.RawMessage(before),
		"after":       json.RawMessage(afterJSON),
		"beforeItems": sample.Items(),
		"afterItems":  after.Items(),
	})
}

func maskSecret(v string) string {
	if v == "" || config.IsEnvRef(v) {
		return v
	}
	return SecretMask
}

func maskTarget(t config.Target) config.Target {
	if t.Headers != nil {
		h := make(map[string]string, len(t.Headers))
		for k, v := range t.Headers {
			h[k] = maskSecret(v)
		}
		t.Headers = h
	}
	if t.Willow != nil {
		w := *t.Willow
		w.Token = maskSecret(w.Token)
		t.Willow = &w
	}
	t.Filters = slices.Clone(t.Filters)
	for i := range t.Filters {
		t.Filters[i].Salt = maskSecret(t.Filters[i].Salt)
	}
	return t
}

// unmaskTarget restores secrets the UI sent back as SecretMask.
func unmaskTarget(in, prev config.Target) config.Target {
	if in.Headers != nil {
		h := make(map[string]string, len(in.Headers))
		for k, v := range in.Headers {
			if v == SecretMask {
				v = prev.Headers[k]
			}
			h[k] = v
		}
		in.Headers = h
	}
	if in.Willow != nil && in.Willow.Token == SecretMask && prev.Willow != nil {
		w := *in.Willow
		w.Token = prev.Willow.Token
		in.Willow = &w
	}
	in.Filters = slices.Clone(in.Filters)
	for i := range in.Filters {
		if in.Filters[i].Salt == SecretMask && i < len(prev.Filters) {
			in.Filters[i].Salt = prev.Filters[i].Salt
		}
	}
	return in
}

func (s *server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.UI == nil {
		http.Error(w, "UI not built", http.StatusNotFound)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if f, err := s.UI.Open(name); err == nil {
		st, _ := f.Stat()
		_ = f.Close()
		if st != nil && !st.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.UI, name)
			return
		}
	}
	// Single-page app: unknown paths get index.html.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, s.UI, "index.html")
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
