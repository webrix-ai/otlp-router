package router_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/webrix-ai/otlp-router/internal/router"
	"github.com/webrix-ai/otlp-router/internal/store"
)

func newRouter(t *testing.T, st store.Store) *router.Router {
	t.Helper()
	rt := router.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(rt.Close)
	if err := rt.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	return rt
}

func TestIngestTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	st := &store.Memory{}
	rt := newRouter(t, st)

	if rt.AuthorizeIngest("") || rt.AuthorizeIngest("anything") {
		t.Fatal("with no tokens, ingest must be rejected rather than open")
	}

	laptop, tok, err := rt.CreateIngestToken(ctx, "Laptops")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(laptop, "otr_") || !strings.HasPrefix(laptop, tok.Hint) || !rt.AuthorizeIngest(laptop) {
		t.Fatalf("new token not accepted: %q %+v", laptop, tok)
	}
	if rt.IngestAuth().LastUsed[tok.ID] == 0 {
		t.Fatal("last use not recorded")
	}

	stored, _, _, _ := st.Load(ctx)
	if len(stored.IngestTokens) != 1 || stored.IngestTokens[0].Hash == laptop || stored.IngestTokens[0].Hash == "" {
		t.Fatalf("only the hash may be stored: %+v", stored.IngestTokens)
	}

	rotated, next, err := rt.RotateIngestToken(ctx, tok.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if next.Name != "Laptops" || !rt.AuthorizeIngest(rotated) || !rt.AuthorizeIngest(laptop) {
		t.Fatal("during the grace period both tokens must work")
	}
	if exp := rt.IngestAuth().Tokens[0].ExpiresAt; exp == nil || time.Until(*exp) > time.Hour {
		t.Fatalf("old token should expire within the grace period: %v", exp)
	}

	again, _, err := rt.RotateIngestToken(ctx, next.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rt.AuthorizeIngest(rotated) || !rt.AuthorizeIngest(again) {
		t.Fatal("rotating with no grace must stop the old token at once")
	}

	ci, ciTok, _ := rt.CreateIngestToken(ctx, "CI")
	if err := rt.RevokeIngestToken(ctx, ciTok.ID); err != nil {
		t.Fatal(err)
	}
	if rt.AuthorizeIngest(ci) {
		t.Fatal("revoked token still accepted")
	}
	if err := rt.RevokeIngestToken(ctx, ciTok.ID); err != router.ErrTokenNotFound {
		t.Fatalf("revoking twice: %v", err)
	}
	if _, _, err := rt.RotateIngestToken(ctx, "nope", 0); err != router.ErrTokenNotFound {
		t.Fatalf("rotating unknown: %v", err)
	}
	if _, _, err := rt.CreateIngestToken(ctx, "  "); err == nil {
		t.Fatal("a name is required")
	}
	if got := rt.IngestAuth().Rejected; got < 4 {
		t.Fatalf("rejections not counted: %d", got)
	}
}

func TestIngestTokensSharedAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	st := &store.Memory{}
	a := newRouter(t, st)
	b := newRouter(t, st)

	// b's cached version goes stale once a saves; its save must retry.
	if _, _, err := a.CreateIngestToken(ctx, "From A"); err != nil {
		t.Fatal(err)
	}
	fromB, _, err := b.CreateIngestToken(ctx, "From B")
	if err != nil {
		t.Fatalf("conflicting save should be retried: %v", err)
	}
	stored, _, _, _ := st.Load(ctx)
	if len(stored.IngestTokens) != 2 {
		t.Fatalf("a token was lost: %+v", stored.IngestTokens)
	}
	deadline := time.Now().Add(7 * time.Second)
	for !a.AuthorizeIngest(fromB) {
		if time.Now().After(deadline) {
			t.Fatal("replica A never picked up B's token")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestStaticAndOpenIngest(t *testing.T) {
	rt := newRouter(t, &store.Memory{})
	rt.SetIngestAuth([]string{"from-config"}, false)
	if !rt.AuthorizeIngest("from-config") || rt.AuthorizeIngest("from-confi") {
		t.Fatal("config tokens must match exactly")
	}
	rt.SetIngestAuth(nil, true)
	if !rt.AuthorizeIngest("") {
		t.Fatal("allowUnauthenticatedIngest should accept requests without a token")
	}
}
