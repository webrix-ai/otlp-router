package telemetry

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

type Level string

const (
	LevelResource Level = "resource"
	LevelRecord   Level = "record"
)

// WalkAttributes calls fn for every attribute map in the payload: resource
// attributes, and record-level ones (log records, spans, span events, span
// links and metric data points).
func (p Payload) WalkAttributes(fn func(m pcommon.Map, level Level)) {
	switch p.Signal {
	case Traces:
		rs := p.Traces.ResourceSpans()
		for i := 0; i < rs.Len(); i++ {
			r := rs.At(i)
			fn(r.Resource().Attributes(), LevelResource)
			for j := 0; j < r.ScopeSpans().Len(); j++ {
				spans := r.ScopeSpans().At(j).Spans()
				for k := 0; k < spans.Len(); k++ {
					s := spans.At(k)
					fn(s.Attributes(), LevelRecord)
					for e := 0; e < s.Events().Len(); e++ {
						fn(s.Events().At(e).Attributes(), LevelRecord)
					}
					for l := 0; l < s.Links().Len(); l++ {
						fn(s.Links().At(l).Attributes(), LevelRecord)
					}
				}
			}
		}
	case Metrics:
		rm := p.Metrics.ResourceMetrics()
		for i := 0; i < rm.Len(); i++ {
			r := rm.At(i)
			fn(r.Resource().Attributes(), LevelResource)
			for j := 0; j < r.ScopeMetrics().Len(); j++ {
				ms := r.ScopeMetrics().At(j).Metrics()
				for k := 0; k < ms.Len(); k++ {
					walkDataPoints(ms.At(k), func(m pcommon.Map) { fn(m, LevelRecord) })
				}
			}
		}
	case Logs:
		rl := p.Logs.ResourceLogs()
		for i := 0; i < rl.Len(); i++ {
			r := rl.At(i)
			fn(r.Resource().Attributes(), LevelResource)
			for j := 0; j < r.ScopeLogs().Len(); j++ {
				lrs := r.ScopeLogs().At(j).LogRecords()
				for k := 0; k < lrs.Len(); k++ {
					fn(lrs.At(k).Attributes(), LevelRecord)
				}
			}
		}
	}
}

// WalkLogBodies calls fn for every log record body.
func (p Payload) WalkLogBodies(fn func(v pcommon.Value)) {
	if p.Signal != Logs {
		return
	}
	rl := p.Logs.ResourceLogs()
	for i := 0; i < rl.Len(); i++ {
		sls := rl.At(i).ScopeLogs()
		for j := 0; j < sls.Len(); j++ {
			lrs := sls.At(j).LogRecords()
			for k := 0; k < lrs.Len(); k++ {
				fn(lrs.At(k).Body())
			}
		}
	}
}

// RemoveLogRecordsIf drops matching log records and prunes emptied scopes and
// resources.
func (p Payload) RemoveLogRecordsIf(drop func(plog.LogRecord) bool) {
	if p.Signal != Logs {
		return
	}
	p.Logs.ResourceLogs().RemoveIf(func(r plog.ResourceLogs) bool {
		r.ScopeLogs().RemoveIf(func(s plog.ScopeLogs) bool {
			s.LogRecords().RemoveIf(drop)
			return s.LogRecords().Len() == 0
		})
		return r.ScopeLogs().Len() == 0
	})
}

func (p Payload) RemoveSpansIf(drop func(ptrace.Span) bool) {
	if p.Signal != Traces {
		return
	}
	p.Traces.ResourceSpans().RemoveIf(func(r ptrace.ResourceSpans) bool {
		r.ScopeSpans().RemoveIf(func(s ptrace.ScopeSpans) bool {
			s.Spans().RemoveIf(drop)
			return s.Spans().Len() == 0
		})
		return r.ScopeSpans().Len() == 0
	})
}

func (p Payload) RemoveMetricsIf(drop func(pmetric.Metric) bool) {
	if p.Signal != Metrics {
		return
	}
	p.Metrics.ResourceMetrics().RemoveIf(func(r pmetric.ResourceMetrics) bool {
		r.ScopeMetrics().RemoveIf(func(s pmetric.ScopeMetrics) bool {
			s.Metrics().RemoveIf(drop)
			return s.Metrics().Len() == 0
		})
		return r.ScopeMetrics().Len() == 0
	})
}

func walkDataPoints(m pmetric.Metric, fn func(pcommon.Map)) {
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		dps := m.Gauge().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			fn(dps.At(i).Attributes())
		}
	case pmetric.MetricTypeSum:
		dps := m.Sum().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			fn(dps.At(i).Attributes())
		}
	case pmetric.MetricTypeHistogram:
		dps := m.Histogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			fn(dps.At(i).Attributes())
		}
	case pmetric.MetricTypeExponentialHistogram:
		dps := m.ExponentialHistogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			fn(dps.At(i).Attributes())
		}
	case pmetric.MetricTypeSummary:
		dps := m.Summary().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			fn(dps.At(i).Attributes())
		}
	}
}

func dataPointCount(m pmetric.Metric) int {
	n := 0
	walkDataPoints(m, func(pcommon.Map) { n++ })
	return n
}
