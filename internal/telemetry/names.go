package telemetry

import (
	"regexp"
	"sort"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const maxNameLen = 128

// identifier matches a log body that is an event name such as
// claude_code.api_request rather than a message.
var identifier = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,128}$`)

// Names counts span names, metric names or log event names, keeping the
// limit most frequent. They name what was sent without its content.
func (p Payload) Names(limit int) map[string]int {
	counts := map[string]int{}
	add := func(n string, c int) {
		if n == "" {
			return
		}
		if len(n) > maxNameLen {
			n = n[:maxNameLen]
		}
		counts[n] += c
	}
	switch p.Signal {
	case Traces:
		rs := p.Traces.ResourceSpans()
		for i := 0; i < rs.Len(); i++ {
			ss := rs.At(i).ScopeSpans()
			for j := 0; j < ss.Len(); j++ {
				spans := ss.At(j).Spans()
				for k := 0; k < spans.Len(); k++ {
					add(spans.At(k).Name(), 1)
				}
			}
		}
	case Metrics:
		rm := p.Metrics.ResourceMetrics()
		for i := 0; i < rm.Len(); i++ {
			sm := rm.At(i).ScopeMetrics()
			for j := 0; j < sm.Len(); j++ {
				ms := sm.At(j).Metrics()
				for k := 0; k < ms.Len(); k++ {
					add(ms.At(k).Name(), dataPointCount(ms.At(k)))
				}
			}
		}
	case Logs:
		rl := p.Logs.ResourceLogs()
		for i := 0; i < rl.Len(); i++ {
			sl := rl.At(i).ScopeLogs()
			for j := 0; j < sl.Len(); j++ {
				lrs := sl.At(j).LogRecords()
				for k := 0; k < lrs.Len(); k++ {
					add(logEventName(lrs.At(k)), 1)
				}
			}
		}
	}
	if len(counts) <= limit {
		return counts
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	top := make(map[string]int, limit)
	for _, k := range keys[:limit] {
		top[k] = counts[k]
	}
	return top
}

func logEventName(lr plog.LogRecord) string {
	if n := lr.EventName(); n != "" {
		return n
	}
	if v, ok := lr.Attributes().Get("event.name"); ok {
		return v.AsString()
	}
	if lr.Body().Type() == pcommon.ValueTypeStr && identifier.MatchString(lr.Body().Str()) {
		return lr.Body().Str()
	}
	return ""
}
