package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/errgroup"

	"github.com/webrix-ai/otlp-router/internal/admin"
	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/ingest"
	"github.com/webrix-ai/otlp-router/internal/router"
	"github.com/webrix-ai/otlp-router/internal/store"
	"github.com/webrix-ai/otlp-router/web"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		hashPassword()
		return
	}
	configPath := flag.String("config", os.Getenv("OTLP_ROUTER_CONFIG"), "path to the config file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(*configPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	st, err := store.New(cfg.Store)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rt := router.New(st, log)
	rt.SetIngestAuth(cfg.Auth.IngestTokens, cfg.Auth.AllowUnauthenticatedIngest)
	if err := rt.Init(ctx, cfg.Targets); err != nil {
		return fmt.Errorf("load targets: %w", err)
	}
	defer rt.Close()

	adminHandler, err := admin.Handler(admin.Options{
		Config: cfg, Router: rt, Store: st, UI: web.FS(), Version: version, Log: log,
	})
	if err != nil {
		return err
	}
	in := &ingest.Server{Router: rt, Authorize: rt.AuthorizeIngest, Log: log}
	if cfg.Auth.AllowUnauthenticatedIngest {
		log.Warn("ingest auth is disabled; anyone who can reach the OTLP ports can send telemetry")
	} else if len(cfg.Auth.IngestTokens) == 0 && len(rt.IngestAuth().Tokens) == 0 {
		log.Warn("no ingest tokens yet; telemetry is rejected until you create one in the UI")
	}
	if cfg.Auth.SessionSecret == "" {
		log.Warn("auth.sessionSecret is unset; UI sessions end on restart and are not shared between replicas")
	}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return serveHTTP(ctx, cfg.Listen.OTLPHTTP, in.HTTPHandler()) })
	g.Go(func() error { return serveHTTP(ctx, cfg.Listen.Admin, adminHandler) })
	g.Go(func() error { return in.ServeGRPC(ctx, cfg.Listen.OTLPGRPC) })
	log.Info("otlp-router started",
		"version", version, "otlpGrpc", cfg.Listen.OTLPGRPC, "otlpHttp", cfg.Listen.OTLPHTTP,
		"admin", cfg.Listen.Admin, "store", st.Kind(), "readOnly", cfg.ReadOnly)
	return g.Wait()
}

func serveHTTP(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return nil
}

// hashPassword prints a bcrypt hash for auth.admin.passwordHash.
func hashPassword() {
	fmt.Fprint(os.Stderr, "Password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "no password read")
		os.Exit(1)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(strings.TrimRight(line, "\r\n")), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(h))
}
