// Package ingest receives OTLP over HTTP and gRPC and hands payloads to the
// router.
package ingest

import (
	"compress/gzip"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip" // accept gzip-compressed gRPC exports
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/webrix-ai/otlp-router/internal/telemetry"
)

const maxBodyBytes = 32 << 20

type Router interface {
	Route(p telemetry.Payload)
}

type Server struct {
	Router Router
	// Tokens accepted as bearer tokens. Empty disables ingest auth.
	Tokens []string
	Log    *slog.Logger
}

func (s *Server) authorized(header string) bool {
	if len(s.Tokens) == 0 {
		return true
	}
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		token = header
	}
	token = strings.TrimSpace(token)
	for _, t := range s.Tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(t)) == 1 {
			return true
		}
	}
	return false
}

// passthrough keeps the inbound x-* headers targets may forward, e.g.
// x-device-id. Credentials never start with x- in OTLP exporters.
func passthrough(get func(yield func(k, v string))) map[string]string {
	out := map[string]string{}
	get(func(k, v string) {
		k = strings.ToLower(k)
		if strings.HasPrefix(k, "x-") && v != "" {
			out[k] = v
		}
	})
	return out
}

func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	for _, sig := range telemetry.AllSignals {
		mux.HandleFunc("POST /v1/"+string(sig), func(w http.ResponseWriter, r *http.Request) { s.serveHTTP(w, r, sig) })
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "willow otlp-router: POST OTLP to /v1/traces, /v1/metrics or /v1/logs\n")
	})
	return mux
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request, sig telemetry.Signal) {
	if !s.authorized(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	enc, ok := telemetry.EncodingFromContentType(r.Header.Get("Content-Type"))
	if !ok {
		http.Error(w, "unsupported content type, use application/x-protobuf or application/json", http.StatusUnsupportedMediaType)
		return
	}
	var body io.Reader = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "invalid gzip body", http.StatusBadRequest)
			return
		}
		defer zr.Close()
		body = io.LimitReader(zr, maxBodyBytes)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	p, err := telemetry.Decode(sig, enc, raw)
	if err != nil {
		// 400 tells the exporter not to retry a payload that can never parse.
		http.Error(w, fmt.Sprintf("invalid OTLP %s payload: %v", sig, err), http.StatusBadRequest)
		return
	}
	p.Headers = passthrough(func(yield func(k, v string)) {
		for k := range r.Header {
			yield(k, r.Header.Get(k))
		}
	})
	s.Router.Route(p)
	writeHTTPResponse(w, sig, enc)
}

func writeHTTPResponse(w http.ResponseWriter, sig telemetry.Signal, enc telemetry.Encoding) {
	var body []byte
	var err error
	switch sig {
	case telemetry.Traces:
		res := ptraceotlp.NewExportResponse()
		if enc == telemetry.JSON {
			body, err = res.MarshalJSON()
		} else {
			body, err = res.MarshalProto()
		}
	case telemetry.Metrics:
		res := pmetricotlp.NewExportResponse()
		if enc == telemetry.JSON {
			body, err = res.MarshalJSON()
		} else {
			body, err = res.MarshalProto()
		}
	default:
		res := plogotlp.NewExportResponse()
		if enc == telemetry.JSON {
			body, err = res.MarshalJSON()
		} else {
			body, err = res.MarshalProto()
		}
	}
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", enc.ContentType())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// GRPCServer returns a gRPC server with the three OTLP services registered.
func (s *Server) GRPCServer() *grpc.Server {
	srv := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxBodyBytes),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			auth := ""
			if v := md.Get("authorization"); len(v) > 0 {
				auth = v[0]
			}
			if !s.authorized(auth) {
				return nil, status.Error(codes.Unauthenticated, "invalid or missing ingest token")
			}
			return h(ctx, req)
		}),
	)
	ptraceotlp.RegisterGRPCServer(srv, &tracesService{s: s})
	pmetricotlp.RegisterGRPCServer(srv, &metricsService{s: s})
	plogotlp.RegisterGRPCServer(srv, &logsService{s: s})
	return srv
}

func grpcHeaders(ctx context.Context) map[string]string {
	md, _ := metadata.FromIncomingContext(ctx)
	return passthrough(func(yield func(k, v string)) {
		for k, v := range md {
			if len(v) > 0 {
				yield(k, v[0])
			}
		}
	})
}

type tracesService struct {
	ptraceotlp.UnimplementedGRPCServer
	s *Server
}

func (t *tracesService) Export(ctx context.Context, req ptraceotlp.ExportRequest) (ptraceotlp.ExportResponse, error) {
	p := telemetry.FromTraces(req.Traces())
	p.Headers = grpcHeaders(ctx)
	t.s.Router.Route(p)
	return ptraceotlp.NewExportResponse(), nil
}

type metricsService struct {
	pmetricotlp.UnimplementedGRPCServer
	s *Server
}

func (m *metricsService) Export(ctx context.Context, req pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error) {
	p := telemetry.FromMetrics(req.Metrics())
	p.Headers = grpcHeaders(ctx)
	m.s.Router.Route(p)
	return pmetricotlp.NewExportResponse(), nil
}

type logsService struct {
	plogotlp.UnimplementedGRPCServer
	s *Server
}

func (l *logsService) Export(ctx context.Context, req plogotlp.ExportRequest) (plogotlp.ExportResponse, error) {
	p := telemetry.FromLogs(req.Logs())
	p.Headers = grpcHeaders(ctx)
	l.s.Router.Route(p)
	return plogotlp.NewExportResponse(), nil
}

// ServeGRPC listens on addr until ctx is done.
func (s *Server) ServeGRPC(ctx context.Context, addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := s.GRPCServer()
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	return srv.Serve(lis)
}
