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

func TestEmptyIngestTokenMeansOpen(t *testing.T) {
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

func TestNormalizeTargets(t *testing.T) {
	out, err := NormalizeTargets([]Target{
		{Name: "Grafana Cloud", Type: TypeOTLPHTTP, Endpoint: "https://x"},
		{Name: "W", Type: TypeWillow, Endpoint: "https://ignored", Willow: &WillowSettings{Org: " acme ", Token: "t"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].ID != "grafana-cloud" || out[0].Encoding != "proto" || !out[0].Signals.Logs {
		t.Fatalf("defaults not applied: %+v", out[0])
	}
	w := out[1]
	if w.Endpoint != "" || w.ResolvedEndpoint() != "https://acme.withwillow.ai/otlp" || w.Encoding != "json" {
		t.Fatalf("willow target: %+v -> %s", w, w.ResolvedEndpoint())
	}

	bad := [][]Target{
		{{Name: "", Type: TypeOTLPHTTP, Endpoint: "x"}},
		{{Name: "a", Type: "kafka", Endpoint: "x"}},
		{{Name: "a", Type: TypeOTLPHTTP}},
		{{Name: "a", Type: TypeWillow, Willow: &WillowSettings{Org: "acme"}}},
		{{Name: "a", Type: TypeOTLPHTTP, Endpoint: "x"}, {Name: "A", Type: TypeOTLPHTTP, Endpoint: "y"}},
	}
	for i, ts := range bad {
		if _, err := NormalizeTargets(ts); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}
