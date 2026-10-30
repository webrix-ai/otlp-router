package telemetry

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// Unknown is the agent slug for telemetry with no recognizable identity.
const Unknown = "unknown"

// agentAliases folds the service.name values each AI agent reports onto one
// slug. Kept in step with Willow's ingest so both sides name agents the same.
var agentAliases = map[string]string{
	"claude-code":        "claude-code",
	"claudecode":         "claude-code",
	"claude":             "claude-code",
	"cowork":             "claude-cowork",
	"claude-cowork":      "claude-cowork",
	"codex":              "codex",
	"codex-cli":          "codex",
	"codex_cli_rs":       "codex",
	"openai-codex":       "codex",
	"codex-app-server":   "codex",
	"gemini":             "gemini-cli",
	"gemini-cli":         "gemini-cli",
	"gemini-code-assist": "gemini-cli",
	"qwen-code":          "qwen-code",
	"cursor":             "cursor",
	"cursor-cli":         "cursor",
	"cursor-agent":       "cursor",
	"pi":                 "pi",
	"pi-coding-agent":    "pi",
	"github-copilot":     "github-copilot",
	"copilot":            "github-copilot",
	"copilot-chat":       "github-copilot",
	"copilotcli":         "github-copilot",
}

// DetectAgent returns the agent slug for a resource, preferring service.name
// and falling back to the instrumentation scope name some exporters use.
func DetectAgent(resource pcommon.Resource, scopeName string) string {
	name := ""
	if v, ok := resource.Attributes().Get("service.name"); ok {
		name = v.AsString()
	}
	if name == "" || strings.HasPrefix(name, "unknown_service") {
		name = scopeName
	}
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = strings.ReplaceAll(slug, " ", "-")
	if slug == "" {
		return Unknown
	}
	if a, ok := agentAliases[slug]; ok {
		return a
	}
	return slug
}

// AgentCounts returns items per detected agent in the payload.
func (p Payload) AgentCounts() map[string]int {
	out := map[string]int{}
	switch p.Signal {
	case Traces:
		rs := p.Traces.ResourceSpans()
		for i := 0; i < rs.Len(); i++ {
			r := rs.At(i)
			for j := 0; j < r.ScopeSpans().Len(); j++ {
				ss := r.ScopeSpans().At(j)
				out[DetectAgent(r.Resource(), ss.Scope().Name())] += ss.Spans().Len()
			}
		}
	case Metrics:
		rm := p.Metrics.ResourceMetrics()
		for i := 0; i < rm.Len(); i++ {
			r := rm.At(i)
			for j := 0; j < r.ScopeMetrics().Len(); j++ {
				sm := r.ScopeMetrics().At(j)
				n := 0
				for k := 0; k < sm.Metrics().Len(); k++ {
					n += dataPointCount(sm.Metrics().At(k))
				}
				out[DetectAgent(r.Resource(), sm.Scope().Name())] += n
			}
		}
	case Logs:
		rl := p.Logs.ResourceLogs()
		for i := 0; i < rl.Len(); i++ {
			r := rl.At(i)
			for j := 0; j < r.ScopeLogs().Len(); j++ {
				sl := r.ScopeLogs().At(j)
				out[DetectAgent(r.Resource(), sl.Scope().Name())] += sl.LogRecords().Len()
			}
		}
	}
	return out
}

// KeepAgents drops every scope whose detected agent is not in allow, then any
// resource left with no scopes. An empty allow list keeps everything.
func (p Payload) KeepAgents(allow []string) {
	if len(allow) == 0 {
		return
	}
	set := make(map[string]bool, len(allow))
	for _, a := range allow {
		set[a] = true
	}
	switch p.Signal {
	case Traces:
		p.Traces.ResourceSpans().RemoveIf(func(r ptrace.ResourceSpans) bool {
			r.ScopeSpans().RemoveIf(func(s ptrace.ScopeSpans) bool {
				return !set[DetectAgent(r.Resource(), s.Scope().Name())]
			})
			return r.ScopeSpans().Len() == 0
		})
	case Metrics:
		p.Metrics.ResourceMetrics().RemoveIf(func(r pmetric.ResourceMetrics) bool {
			r.ScopeMetrics().RemoveIf(func(s pmetric.ScopeMetrics) bool {
				return !set[DetectAgent(r.Resource(), s.Scope().Name())]
			})
			return r.ScopeMetrics().Len() == 0
		})
	case Logs:
		p.Logs.ResourceLogs().RemoveIf(func(r plog.ResourceLogs) bool {
			r.ScopeLogs().RemoveIf(func(s plog.ScopeLogs) bool {
				return !set[DetectAgent(r.Resource(), s.Scope().Name())]
			})
			return r.ScopeLogs().Len() == 0
		})
	}
}
