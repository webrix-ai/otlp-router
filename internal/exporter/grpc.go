package exporter

import (
	"context"
	"crypto/tls"
	"strings"

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/telemetry"
)

type grpcSender struct {
	conn        *grpc.ClientConn
	traces      ptraceotlp.GRPCClient
	metrics     pmetricotlp.GRPCClient
	logs        plogotlp.GRPCClient
	headers     map[string]string
	passthrough []string
	callOpts    []grpc.CallOption
}

// newGRPCSender accepts "host:port", "https://host:port" (TLS) or
// "http://host:port" (plaintext).
func newGRPCSender(t config.Target) (*grpcSender, error) {
	target := t.Endpoint
	plaintext := false
	switch {
	case strings.HasPrefix(target, "http://"):
		plaintext = true
		target = strings.TrimPrefix(target, "http://")
	case strings.HasPrefix(target, "https://"):
		target = strings.TrimPrefix(target, "https://")
	}
	target = strings.TrimRight(target, "/")

	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: t.Insecure}) //nolint:gosec // opt-in per target
	if plaintext {
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for k, v := range t.Headers {
		headers[strings.ToLower(k)] = config.ExpandEnv(v)
	}
	var opts []grpc.CallOption
	if t.Compression == "gzip" {
		opts = append(opts, grpc.UseCompressor(gzip.Name))
	}
	return &grpcSender{
		conn:        conn,
		traces:      ptraceotlp.NewGRPCClient(conn),
		metrics:     pmetricotlp.NewGRPCClient(conn),
		logs:        plogotlp.NewGRPCClient(conn),
		headers:     headers,
		passthrough: t.PassthroughHeaders,
		callOpts:    opts,
	}, nil
}

func (s *grpcSender) send(ctx context.Context, p telemetry.Payload) error {
	md := metadata.New(nil)
	for _, h := range s.passthrough {
		if v := p.Headers[strings.ToLower(h)]; v != "" {
			md.Set(h, v)
		}
	}
	for k, v := range s.headers {
		md.Set(k, v)
	}
	ctx = metadata.NewOutgoingContext(ctx, md)

	var err error
	switch p.Signal {
	case telemetry.Traces:
		_, err = s.traces.Export(ctx, ptraceotlp.NewExportRequestFromTraces(p.Traces), s.callOpts...)
	case telemetry.Metrics:
		_, err = s.metrics.Export(ctx, pmetricotlp.NewExportRequestFromMetrics(p.Metrics), s.callOpts...)
	case telemetry.Logs:
		_, err = s.logs.Export(ctx, plogotlp.NewExportRequestFromLogs(p.Logs), s.callOpts...)
	}
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.ResourceExhausted, codes.DeadlineExceeded,
		codes.Aborted, codes.OutOfRange, codes.DataLoss, codes.Canceled:
		return err
	}
	return permanentError{err}
}

func (s *grpcSender) close() error { return s.conn.Close() }
