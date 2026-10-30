// send-sample sends realistic AI-agent telemetry to a router, for demos and
// manual testing: Claude Code logs and metrics over HTTP/protobuf, Codex logs
// over HTTP/JSON and Gemini CLI spans over gRPC.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	httpURL := flag.String("http", "http://localhost:4318", "OTLP/HTTP base URL")
	grpcAddr := flag.String("grpc", "localhost:4317", "OTLP/gRPC address")
	token := flag.String("token", "", "ingest token")
	rounds := flag.Int("n", 1, "rounds to send")
	flag.Parse()

	for i := 0; i < *rounds; i++ {
		post(*httpURL+"/v1/logs", "application/x-protobuf", *token, must((&plog.ProtoMarshaler{}).MarshalLogs(claudeLogs())))
		post(*httpURL+"/v1/metrics", "application/x-protobuf", *token, must((&pmetric.ProtoMarshaler{}).MarshalMetrics(claudeMetrics())))
		post(*httpURL+"/v1/logs", "application/json", *token, must((&plog.JSONMarshaler{}).MarshalLogs(codexLogs())))
		sendGRPC(*grpcAddr, *token, geminiTraces())
		if i < *rounds-1 {
			time.Sleep(time.Second)
		}
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		log.Fatal(err)
	}
	return b
}

func post(url, contentType, token string, body []byte) {
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Device-Id", "mbp-dana-7f3a")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	res.Body.Close()
	fmt.Println("POST", url, contentType, res.Status)
}

func sendGRPC(addr, token string, td ptrace.Traces) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	}
	_, err = ptraceotlp.NewGRPCClient(conn).Export(ctx, ptraceotlp.NewExportRequestFromTraces(td))
	fmt.Println("gRPC", addr, "traces", err)
}

func now() pcommon.Timestamp { return pcommon.NewTimestampFromTime(time.Now()) }

func claudeLogs() plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "claude-code")
	rl.Resource().Attributes().PutStr("service.version", "2.4.1")
	rl.Resource().Attributes().PutStr("host.name", "mbp-dana")
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("com.anthropic.claude_code.events")
	add := func(event string, attrs map[string]string) {
		lr := sl.LogRecords().AppendEmpty()
		lr.SetTimestamp(now())
		lr.Body().SetStr("claude_code." + event)
		lr.Attributes().PutStr("event.name", event)
		lr.Attributes().PutStr("user.email", "dana@acme.com")
		lr.Attributes().PutStr("session.id", "b3c1-55e2")
		for k, v := range attrs {
			lr.Attributes().PutStr(k, v)
		}
	}
	add("user_prompt", map[string]string{"prompt": "rotate the staging key sk-ant-api03-abcdefghijklmnopqrstuvwxyz and redeploy", "prompt_length": "71"})
	add("api_request", map[string]string{"model": "claude-sonnet-4-5", "cost_usd": "0.0123", "input_tokens": "1830", "output_tokens": "412"})
	add("tool_result", map[string]string{"tool_name": "Bash", "success": "true", "tool_parameters": `{"command":"kubectl rollout restart deploy/api"}`})
	return ld
}

func claudeMetrics() pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", "claude-code")
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("com.anthropic.claude_code")
	for name, v := range map[string]float64{"claude_code.cost.usage": 0.0123, "claude_code.token.usage": 2242} {
		m := sm.Metrics().AppendEmpty()
		m.SetName(name)
		sum := m.SetEmptySum()
		sum.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
		dp := sum.DataPoints().AppendEmpty()
		dp.SetTimestamp(now())
		dp.SetDoubleValue(v)
		dp.Attributes().PutStr("user.email", "dana@acme.com")
		dp.Attributes().PutStr("model", "claude-sonnet-4-5")
	}
	return md
}

func codexLogs() plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "codex_cli_rs")
	sl := rl.ScopeLogs().AppendEmpty()
	for _, event := range []string{"codex.conversation_starts", "codex.user_prompt", "codex.api_request"} {
		lr := sl.LogRecords().AppendEmpty()
		lr.SetTimestamp(now())
		lr.SetEventName(event)
		lr.Attributes().PutStr("event.name", event)
		lr.Attributes().PutStr("user.email", "omer@acme.com")
		if event == "codex.user_prompt" {
			lr.Attributes().PutStr("prompt", "why is the billing job failing?")
		}
	}
	return ld
}

func geminiTraces() ptrace.Traces {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "gemini-cli")
	ss := rs.ScopeSpans().AppendEmpty()
	span := ss.Spans().AppendEmpty()
	span.SetName("gemini_cli.api_request")
	span.SetTraceID(pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}))
	span.SetSpanID(pcommon.SpanID([8]byte{1, 2, 3, 4, 5, 6, 7, 8}))
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Now().Add(-800 * time.Millisecond)))
	span.SetEndTimestamp(now())
	span.Attributes().PutStr("gen_ai.request.model", "gemini-2.5-pro")
	span.Attributes().PutStr("user.email", "lior@acme.com")
	return td
}
