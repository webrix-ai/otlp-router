package exporter

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/telemetry"
)

type httpSender struct {
	baseURL     string
	encoding    telemetry.Encoding
	gzip        bool
	headers     map[string]string
	passthrough []string
	client      *http.Client
}

func newHTTPSender(t config.Target) (*httpSender, error) {
	base := strings.TrimRight(t.ResolvedEndpoint(), "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, fmt.Errorf("endpoint must start with http:// or https://")
	}
	headers := map[string]string{}
	for k, v := range t.Headers {
		headers[k] = config.ExpandEnv(v)
	}
	if t.Type == config.TypeWillow && t.Willow != nil {
		headers["Authorization"] = "Bearer " + config.ExpandEnv(t.Willow.Token)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if t.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in per target
	}
	enc := telemetry.Proto
	if t.Encoding == "json" {
		enc = telemetry.JSON
	}
	return &httpSender{
		baseURL:     base,
		encoding:    enc,
		gzip:        t.Compression == "gzip",
		headers:     headers,
		passthrough: t.PassthroughHeaders,
		client:      &http.Client{Transport: transport},
	}, nil
}

func (s *httpSender) send(ctx context.Context, p telemetry.Payload) error {
	body, err := p.Encode(s.encoding)
	if err != nil {
		return permanentError{fmt.Errorf("encode: %w", err)}
	}
	if s.gzip {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(body)
		_ = zw.Close()
		body = buf.Bytes()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/"+string(p.Signal), bytes.NewReader(body))
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("Content-Type", s.encoding.ContentType())
	req.Header.Set("User-Agent", "willow-otlp-router")
	if s.gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	for _, h := range s.passthrough {
		if v := p.Headers[strings.ToLower(h)]; v != "" {
			req.Header.Set(h, v)
		}
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	err = fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(snippet)))
	switch res.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return err
	}
	return permanentError{err}
}

func (s *httpSender) close() error {
	s.client.CloseIdleConnections()
	return nil
}

func emptyLogs() plog.Logs { return plog.NewLogs() }
