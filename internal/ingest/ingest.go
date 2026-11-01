// Package ingest receives OTLP over HTTP and gRPC and hands payloads to the
// router.
package ingest

import (
	"compress/gzip"
	"context"
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
	// Rejected records a request that was turned away.
	Rejected(sig telemetry.Signal, reason string, headers map[string]string)
}

type Server struct {
	Router Router
	// Authorize checks the bearer token an agent sent and names it. Nil
	// accepts everything.
	Authorize func(token string) (name string, ok bool)
	Log       *slog.Logger
}

func (s *Server) authorized(header string) (string, bool) {
	if s.Authorize == nil {
		return "", true
	}
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		token = header
	}
	return s.Authorize(strings.TrimSpace(token))
}

const (
	reasonUnauthorized = "missing or invalid ingest token"
	reasonContentType  = "unsupported content type"
)

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

func httpHeaders(r *http.Request) map[string]string {
	return passthrough(func(yield func(k, v string)) {
		for k := range r.Header {
			yield(k, r.Header.Get(k))
		}
	})
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request, sig telemetry.Signal) {
	tokenName, ok := s.authorized(r.Header.Get("Authorization"))
	if !ok {
		s.Router.Rejected(sig, reasonUnauthorized, httpHeaders(r))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	enc, ok := telemetry.EncodingFromContentType(r.Header.Get("Content-Type"))
	if !ok {
		s.Router.Rejected(sig, reasonContentType+" "+r.Header.Get("Content-Type"), httpHeaders(r))
		http.Error(w, "unsupported content type, use application/x-protobuf or application/json", http.StatusUnsupportedMediaType)
		return
	}
	var body io.Reader = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(body)
		if err != nil {
			s.Router.Rejected(sig, "invalid gzip body", httpHeaders(r))
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
			s.Router.Rejected(sig, "body too large", httpHeaders(r))
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		s.Router.Rejected(sig, "read body: "+err.Error(), httpHeaders(r))
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	p, err := telemetry.Decode(sig, enc, raw)
	if err != nil {
		msg := fmt.Sprintf("invalid OTLP %s payload: %v", sig, err)
		s.Router.Rejected(sig, msg, httpHeaders(r))
		// 400 tells the exporter not to retry a payload that can never parse.
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	p.Headers = httpHeaders(r)
	p.TokenName = tokenName
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
		grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			auth := ""
			if v := md.Get("authorization"); len(v) > 0 {
				auth = v[0]
			}
			name, ok := s.authorized(auth)
			if !ok {
				s.Router.Rejected(grpcSignal(info.FullMethod), reasonUnauthorized, grpcHeaders(ctx))
				return nil, status.Error(codes.Unauthenticated, "invalid or missing ingest token")
			}
			return h(context.WithValue(ctx, tokenNameKey{}, name), req)
		}),
	)
	ptraceotlp.RegisterGRPCServer(srv, &tracesService{s: s})
	pmetricotlp.RegisterGRPCServer(srv, &metricsService{s: s})
	plogotlp.RegisterGRPCServer(srv, &logsService{s: s})
	return srv
}

type tokenNameKey struct{}

// grpcPayload adds what the interceptor learned about the request.
func grpcPayload(ctx context.Context, p telemetry.Payload) telemetry.Payload {
	p.Headers = grpcHeaders(ctx)
	p.TokenName, _ = ctx.Value(tokenNameKey{}).(string)
	return p
}

// grpcSignal reads the signal from a method such as
// /opentelemetry.proto.collector.logs.v1.LogsService/Export.
func grpcSignal(method string) telemetry.Signal {
	for _, sig := range telemetry.AllSignals {
		if strings.Contains(method, "."+string(sig)+".") {
			return sig
		}
	}
	return ""
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
	p := grpcPayload(ctx, telemetry.FromTraces(req.Traces()))
	t.s.Router.Route(p)
	return ptraceotlp.NewExportResponse(), nil
}

type metricsService struct {
	pmetricotlp.UnimplementedGRPCServer
	s *Server
}

func (m *metricsService) Export(ctx context.Context, req pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error) {
	p := grpcPayload(ctx, telemetry.FromMetrics(req.Metrics()))
	m.s.Router.Route(p)
	return pmetricotlp.NewExportResponse(), nil
}

type logsService struct {
	plogotlp.UnimplementedGRPCServer
	s *Server
}

func (l *logsService) Export(ctx context.Context, req plogotlp.ExportRequest) (plogotlp.ExportResponse, error) {
	p := grpcPayload(ctx, telemetry.FromLogs(req.Logs()))
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
