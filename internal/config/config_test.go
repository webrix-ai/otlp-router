package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleConfigLoads(t *testing.T) {
	t.Setenv("OTLP_ROUTER_ADMIN_PASSWORD", "pw")
	t.Setenv("OTLP_ROUTER_INGEST_TOKENS", "a, b")
	cfg, err := Load("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Admin.Password != "pw" {
		t.Fatalf("password not expanded: %q", cfg.Auth.Admin.Password)
	}
	if len(cfg.Auth.IngestTokens) != 2 || cfg.Auth.IngestTokens[1] != "b" {
		t.Fatalf("comma-separated tokens not split: %q", cfg.Auth.IngestTokens)
	}
	if cfg.Targets[0].Willow.Token != "${env:WILLOW_OTLP_TOKEN}" {
		t.Fatal("target secrets must stay as references so they are stored unexpanded")
	}
}

func TestUnsetIngestTokenIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	raw := "auth:\n  admin:\n    password: x\n  ingestTokens: [\"${env:UNSET_TOKENS}\"]\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Auth.IngestTokens) != 0 {
		t.Fatalf("an unset token variable must not become an empty accepted token: %q", cfg.Auth.IngestTokens)
	}
}

// clearRouterEnv unsets every variable applyEnv reads, so a test sees only
// what it sets.
func clearRouterEnv(t *testing.T) {
	for _, name := range []string{
		"OTLP_ROUTER_PUBLIC_URL", "OTLP_ROUTER_ADMIN_USERNAME", "OTLP_ROUTER_ADMIN_PASSWORD",
		"OTLP_ROUTER_ADMIN_PASSWORD_HASH", "OTLP_ROUTER_SESSION_SECRET", "OTLP_ROUTER_INGEST_TOKENS",
		"OTLP_ROUTER_STORE", "OTLP_ROUTER_STORE_PATH", "OTLP_ROUTER_WILLOW_ORG",
		"OTLP_ROUTER_WILLOW_REGION", "OTLP_ROUTER_WILLOW_CONNECT_URL", "WILLOW_OTLP_TOKEN",
	} {
		t.Setenv(name, "")
	}
}

func TestLoadFromEnvOnly(t *testing.T) {
	clearRouterEnv(t)
	if _, err := Load(""); err == nil {
		t.Fatal("expected an error without an admin password")
	}
	t.Setenv("OTLP_ROUTER_ADMIN_PASSWORD", "pw")
	t.Setenv("OTLP_ROUTER_SESSION_SECRET", "s")
	t.Setenv("OTLP_ROUTER_INGEST_TOKENS", "a, b")
	t.Setenv("OTLP_ROUTER_PUBLIC_URL", "https://otel.acme.internal")
	t.Setenv("OTLP_ROUTER_STORE", "file")
	t.Setenv("OTLP_ROUTER_STORE_PATH", "/data/targets.yaml")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Admin.Username != "admin" || cfg.Auth.Admin.Password != "pw" || cfg.Auth.SessionSecret != "s" {
		t.Fatalf("auth not read from env: %+v", cfg.Auth)
	}
	if len(cfg.Auth.IngestTokens) != 2 || cfg.Auth.IngestTokens[1] != "b" {
		t.Fatalf("tokens not read from env: %q", cfg.Auth.IngestTokens)
	}
	if cfg.PublicURL != "https://otel.acme.internal" || cfg.Store.Type != StoreFile || cfg.Store.Path != "/data/targets.yaml" {
		t.Fatalf("env not applied: %+v", cfg)
	}
	if len(cfg.Targets) != 0 {
		t.Fatalf("no target should be seeded without a Willow org: %+v", cfg.Targets)
	}
}

func TestConfigFileWinsOverEnv(t *testing.T) {
	clearRouterEnv(t)
	t.Setenv("OTLP_ROUTER_ADMIN_PASSWORD", "from-env")
	t.Setenv("OTLP_ROUTER_STORE", "file")
	t.Setenv("OTLP_ROUTER_STORE_PATH", "/data/targets.yaml")
	t.Setenv("OTLP_ROUTER_WILLOW_ORG", "acme")
	t.Setenv("WILLOW_OTLP_TOKEN", "t")
	path := filepath.Join(t.TempDir(), "c.yaml")
	raw := "auth:\n  admin:\n    passwordHash: h\nstore:\n  type: memory\ntargets:\n  - name: x\n    type: otlphttp\n    endpoint: https://x\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Admin.Password != "" || cfg.Auth.Admin.PasswordHash != "h" {
		t.Fatalf("env password must not be added to a file that sets a hash: %+v", cfg.Auth.Admin)
	}
	if cfg.Store.Type != StoreMemory {
		t.Fatalf("file store type overridden: %q", cfg.Store.Type)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].Name != "x" {
		t.Fatalf("targets from the file must not get a Willow target added: %+v", cfg.Targets)
	}
}

func TestWillowTargetFromEnv(t *testing.T) {
	clearRouterEnv(t)
	t.Setenv("OTLP_ROUTER_ADMIN_PASSWORD", "pw")
	t.Setenv("OTLP_ROUTER_WILLOW_ORG", "acme")
	if _, err := Load(""); err == nil {
		t.Fatal("expected an error without WILLOW_OTLP_TOKEN")
	}
	t.Setenv("WILLOW_OTLP_TOKEN", "secret")
	t.Setenv("OTLP_ROUTER_WILLOW_REGION", "eu")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Targets) != 1 {
		t.Fatalf("expected one Willow target: %+v", cfg.Targets)
	}
	w := cfg.Targets[0]
	if w.Type != TypeWillow || !w.Enabled || len(w.Filters) != 2 {
		t.Fatalf("willow target: %+v", w)
	}
	if w.Willow.Token != "${env:WILLOW_OTLP_TOKEN}" {
		t.Fatal("the token must stay a reference so it is never written to the store")
	}
	out, err := NormalizeTargets(cfg.Targets)
	if err != nil {
		t.Fatal(err)
	}
	if got := out[0].ResolvedEndpoint(); got != "https://acme.eu.withwillow.ai/otlp" {
		t.Fatalf("endpoint: %s", got)
	}

	t.Setenv("OTLP_ROUTER_WILLOW_ORG", "")
	t.Setenv("OTLP_ROUTER_WILLOW_REGION", "")
	t.Setenv("OTLP_ROUTER_WILLOW_CONNECT_URL", "https://willow.acme.internal")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	out, err = NormalizeTargets(cfg.Targets)
	if err != nil {
		t.Fatal(err)
	}
	if got := out[0].ResolvedEndpoint(); got != "https://willow.acme.internal/otlp" {
		t.Fatalf("a connect URL alone should mean the custom region: %s", got)
	}
}

func TestNormalizeTargets(t *testing.T) {
	out, err := NormalizeTargets([]Target{
		{Name: "Grafana Cloud", Type: TypeOTLPHTTP, Endpoint: "https://x"},
		{Name: "W", Type: TypeWillow, Endpoint: "https://ignored", Willow: &WillowSettings{Org: " acme ", Token: "t"}},
		{Name: "W EU", Type: TypeWillow, Willow: &WillowSettings{Org: "acme", Token: "t", Region: " EU "}},
		{Name: "W onprem", Type: TypeWillow, Willow: &WillowSettings{Token: "t", Region: "custom", ConnectURL: " https://willow.acme.internal/otlp/v1/logs/ "}},
		{Name: "W prefix", Type: TypeWillow, Willow: &WillowSettings{Token: "t", Region: "custom", ConnectURL: "http://10.0.0.5:3000/willow"}},
		{Name: "W switched", Type: TypeWillow, Willow: &WillowSettings{Org: "acme", Token: "t", Region: "us", ConnectURL: "https://stale"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].ID != "grafana-cloud" || out[0].Encoding != "proto" || !out[0].Signals.Logs {
		t.Fatalf("defaults not applied: %+v", out[0])
	}
	w := out[1]
	if w.Endpoint != "" || w.ResolvedEndpoint() != "https://acme.withwillow.ai/otlp" || w.Encoding != "json" || w.Willow.Region != "us" {
		t.Fatalf("willow target: %+v -> %s", w, w.ResolvedEndpoint())
	}
	if eu := out[2]; eu.ResolvedEndpoint() != "https://acme.eu.withwillow.ai/otlp" || eu.Willow.Region != "eu" {
		t.Fatalf("willow eu target: %+v -> %s", eu.Willow, eu.ResolvedEndpoint())
	}
	for i, want := range map[int]string{3: "https://willow.acme.internal/otlp", 4: "http://10.0.0.5:3000/willow/otlp", 5: "https://acme.withwillow.ai/otlp"} {
		if got := out[i].ResolvedEndpoint(); got != want {
			t.Errorf("%s: got %s, want %s", out[i].Name, got, want)
		}
	}
	if out[5].Willow.ConnectURL != "" {
		t.Error("a hosted region must not keep a stale connectUrl")
	}

	bad := [][]Target{
		{{Name: "", Type: TypeOTLPHTTP, Endpoint: "x"}},
		{{Name: "a", Type: "kafka", Endpoint: "x"}},
		{{Name: "a", Type: TypeOTLPHTTP}},
		{{Name: "a", Type: TypeWillow, Willow: &WillowSettings{Org: "acme"}}},
		{{Name: "a", Type: TypeWillow, Willow: &WillowSettings{Org: "acme", Token: "t", Region: "apac"}}},
		{{Name: "a", Type: TypeWillow, Willow: &WillowSettings{Token: "t"}}},
		{{Name: "a", Type: TypeWillow, Willow: &WillowSettings{Token: "t", Region: "custom"}}},
		{{Name: "a", Type: TypeWillow, Willow: &WillowSettings{Token: "t", Region: "custom", ConnectURL: "willow.acme.internal"}}},
		{{Name: "a", Type: TypeWillow, Willow: &WillowSettings{Token: "t", Region: "custom", ConnectURL: "https://u:p@willow.acme.internal"}}},
		{{Name: "a", Type: TypeOTLPHTTP, Endpoint: "x"}, {Name: "A", Type: TypeOTLPHTTP, Endpoint: "y"}},
	}
	for i, ts := range bad {
		if _, err := NormalizeTargets(ts); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}
