// Package telemetry holds the signal-agnostic OTLP payload the router moves
// between ingest, filters and exporters.
package telemetry

import (
	"fmt"
	"strings"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

type Signal string

const (
	Traces  Signal = "traces"
	Metrics Signal = "metrics"
	Logs    Signal = "logs"
)

var AllSignals = []Signal{Traces, Metrics, Logs}

func ParseSignal(s string) (Signal, error) {
	switch Signal(s) {
	case Traces, Metrics, Logs:
		return Signal(s), nil
	}
	return "", fmt.Errorf("unknown signal %q", s)
}

type Encoding string

const (
	Proto Encoding = "proto"
	JSON  Encoding = "json"
)

// EncodingFromContentType maps an OTLP/HTTP Content-Type to its encoding.
func EncodingFromContentType(ct string) (Encoding, bool) {
	ct = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	switch ct {
	case "application/x-protobuf", "application/protobuf":
		return Proto, true
	case "application/json":
		return JSON, true
	}
	return "", false
}

func (e Encoding) ContentType() string {
	if e == JSON {
		return "application/json"
	}
	return "application/x-protobuf"
}

// Payload is one OTLP export request. Exactly one of Traces, Metrics or Logs
// is meaningful, selected by Signal.
type Payload struct {
	Signal  Signal
	Traces  ptrace.Traces
	Metrics pmetric.Metrics
	Logs    plog.Logs
	// Headers are the inbound request headers (lowercased keys) a target may
	// pass through, e.g. x-device-id. Credentials are never included.
	Headers map[string]string
}

func FromTraces(td ptrace.Traces) Payload    { return Payload{Signal: Traces, Traces: td} }
func FromMetrics(md pmetric.Metrics) Payload { return Payload{Signal: Metrics, Metrics: md} }
func FromLogs(ld plog.Logs) Payload          { return Payload{Signal: Logs, Logs: ld} }

func Decode(signal Signal, enc Encoding, body []byte) (Payload, error) {
	var p Payload
	var err error
	switch signal {
	case Traces:
		var td ptrace.Traces
		if enc == JSON {
			td, err = (&ptrace.JSONUnmarshaler{}).UnmarshalTraces(body)
		} else {
			td, err = (&ptrace.ProtoUnmarshaler{}).UnmarshalTraces(body)
		}
		p = FromTraces(td)
	case Metrics:
		var md pmetric.Metrics
		if enc == JSON {
			md, err = (&pmetric.JSONUnmarshaler{}).UnmarshalMetrics(body)
		} else {
			md, err = (&pmetric.ProtoUnmarshaler{}).UnmarshalMetrics(body)
		}
		p = FromMetrics(md)
	case Logs:
		var ld plog.Logs
		if enc == JSON {
			ld, err = (&plog.JSONUnmarshaler{}).UnmarshalLogs(body)
		} else {
			ld, err = (&plog.ProtoUnmarshaler{}).UnmarshalLogs(body)
		}
		p = FromLogs(ld)
	default:
		return Payload{}, fmt.Errorf("unknown signal %q", signal)
	}
	return p, err
}

func (p Payload) Encode(enc Encoding) ([]byte, error) {
	switch p.Signal {
	case Traces:
		if enc == JSON {
			return (&ptrace.JSONMarshaler{}).MarshalTraces(p.Traces)
		}
		return (&ptrace.ProtoMarshaler{}).MarshalTraces(p.Traces)
	case Metrics:
		if enc == JSON {
			return (&pmetric.JSONMarshaler{}).MarshalMetrics(p.Metrics)
		}
		return (&pmetric.ProtoMarshaler{}).MarshalMetrics(p.Metrics)
	case Logs:
		if enc == JSON {
			return (&plog.JSONMarshaler{}).MarshalLogs(p.Logs)
		}
		return (&plog.ProtoMarshaler{}).MarshalLogs(p.Logs)
	}
	return nil, fmt.Errorf("unknown signal %q", p.Signal)
}

// Clone deep-copies the payload so each target can filter independently.
func (p Payload) Clone() Payload {
	c := Payload{Signal: p.Signal, Headers: p.Headers}
	switch p.Signal {
	case Traces:
		c.Traces = ptrace.NewTraces()
		p.Traces.CopyTo(c.Traces)
	case Metrics:
		c.Metrics = pmetric.NewMetrics()
		p.Metrics.CopyTo(c.Metrics)
	case Logs:
		c.Logs = plog.NewLogs()
		p.Logs.CopyTo(c.Logs)
	}
	return c
}

// Items is the number of spans, data points or log records.
func (p Payload) Items() int {
	switch p.Signal {
	case Traces:
		return p.Traces.SpanCount()
	case Metrics:
		return p.Metrics.DataPointCount()
	case Logs:
		return p.Logs.LogRecordCount()
	}
	return 0
}

// Empty payloads are not sent; OTLP allows them but they carry nothing.
func (p Payload) Empty() bool { return p.Items() == 0 }
