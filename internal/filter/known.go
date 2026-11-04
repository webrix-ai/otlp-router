package filter

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/withwillowai/otlp-router/internal/telemetry"
)

// Category is a kind of data every agent exports under its own names. A
// drop_known rule removes it from each record using the names of the agent
// that sent it; a hash_known rule hashes it instead.
type Category string

const (
	Prompts       Category = "prompts"
	SystemPrompts Category = "system_prompts"
	Responses     Category = "responses"
	ToolArguments Category = "tool_arguments"
	ToolResults   Category = "tool_results"
	Cost          Category = "cost"
	Noise         Category = "noise"
	Identity      Category = "identity"
)

// KnownCategory describes a category for the UI.
type KnownCategory struct {
	ID          Category `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	// Agents are those whose own names for this data the router knows.
	Agents []string `json:"agents"`
	// Standard is true when the category also covers OpenTelemetry's standard
	// names, such as the GenAI conventions GitHub Copilot and SDK agents use.
	Standard bool `json:"standard"`
	// Hashable categories can be used in hash_known rules.
	Hashable bool `json:"hashable"`
	// Removes lists exactly what the category removes, per group of agents.
	Removes []KnownRemoval `json:"removes"`
}

// KnownRemoval is what a category removes from one group of agents.
type KnownRemoval struct {
	// Source is "agent" (Agents lists them), "standard" (OpenTelemetry's
	// standard names, any agent) or "other" (agents without a profile).
	Source     string   `json:"source"`
	Agents     []string `json:"agents,omitempty"`
	Events     []string `json:"events,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
	Metrics    []string `json:"metrics,omitempty"`
	// Bodies are log events kept with their body emptied.
	Bodies []string `json:"bodies,omitempty"`
	Rules  []string `json:"rules,omitempty"`
}

// agentNames says where one agent puts a category's data.
type agentNames struct {
	events     []string // log records and span events, dropped whole
	attributes []string // keys removed from resources, records, span events and data points
	metrics    []string // metrics, dropped whole
	bodies     []string // log records kept with their body emptied
	dropLog    func(plog.LogRecord) bool
	dropLogIs  string // dropLog in words, for the UI
}

func (n agentNames) removal(source string, agents []string) KnownRemoval {
	r := KnownRemoval{Source: source, Agents: agents, Events: n.events, Attributes: n.attributes, Metrics: n.metrics, Bodies: n.bodies}
	if n.dropLogIs != "" {
		r.Rules = []string{n.dropLogIs}
	}
	return r
}

func (n agentNames) empty() bool {
	return len(n.events) == 0 && len(n.attributes) == 0 && len(n.metrics) == 0 && len(n.bodies) == 0 && n.dropLog == nil
}

type knownDef struct {
	meta     KnownCategory
	standard agentNames            // OpenTelemetry standard names, applied to every agent
	agents   map[string]agentNames // each agent's own names, keyed by profile
	unknown  agentNames            // common names, for agents without a profile
}

// profiles are the agents the router knows. Agents sharing another's export
// format map onto its profile.
var profiles = map[string]string{
	"claude-code":    "claude-code",
	"claude-cowork":  "claude-code",
	"codex":          "codex",
	"gemini-cli":     "gemini-cli",
	"qwen-code":      "gemini-cli", // a Gemini CLI fork with the same events
	"github-copilot": "github-copilot",
	// Cursor and Pi have no OpenTelemetry export of their own. The Willow agent
	// sends theirs, with the same records for both.
	"cursor": "cursor",
	"pi":     "cursor",
}

var knownDefs = []knownDef{
	{
		meta: KnownCategory{
			ID:          Prompts,
			Name:        "Prompts",
			Description: "What users type. Prompt events are kept without their text, so prompts can still be counted. For GenAI-convention agents this is the whole input conversation, which can include tool results.",
		},
		standard: agentNames{
			events:     []string{"gen_ai.user.message"},
			attributes: []string{"gen_ai.prompt", "gen_ai.prompt.*", "gen_ai.input.messages"},
		},
		agents: map[string]agentNames{
			"claude-code": {attributes: []string{"prompt"}},
			"codex":       {attributes: []string{"prompt"}},
			"gemini-cli":  {attributes: []string{"prompt", "request_text"}},
			"cursor":      {bodies: []string{"user_prompt"}},
		},
		unknown: agentNames{
			events:     []string{"*user_prompt"},
			attributes: []string{"prompt", "user.prompt"},
		},
	},
	{
		meta: KnownCategory{
			ID:          SystemPrompts,
			Name:        "System prompts",
			Description: "The instructions an agent or SDK gives the model before the conversation.",
		},
		standard: agentNames{
			events:     []string{"gen_ai.system.message"},
			attributes: []string{"gen_ai.system_instructions"},
		},
	},
	{
		meta: KnownCategory{
			ID:          Responses,
			Name:        "Model responses",
			Description: "The text models answer with.",
		},
		standard: agentNames{
			events:     []string{"gen_ai.assistant.message", "gen_ai.choice"},
			attributes: []string{"gen_ai.completion", "gen_ai.completion.*", "gen_ai.output.messages"},
		},
		agents: map[string]agentNames{
			"gemini-cli": {attributes: []string{"response_text"}},
		},
	},
	{
		meta: KnownCategory{
			ID:          ToolArguments,
			Name:        "Tool arguments",
			Description: "What the agent passed to a tool, such as shell commands and file paths. Tool names, decisions and outcomes are kept.",
		},
		standard: agentNames{
			attributes: []string{"gen_ai.tool.call.arguments"},
		},
		agents: map[string]agentNames{
			"claude-code": {attributes: []string{"tool_parameters", "tool_input"}},
			"codex":       {attributes: []string{"arguments"}},
			"gemini-cli":  {attributes: []string{"function_args"}},
			"cursor":      {attributes: []string{"willow.shell.command", "willow.file.path", "willow.detect.tool_input"}},
		},
		unknown: agentNames{
			attributes: []string{"tool_input", "tool_parameters", "tool.arguments"},
		},
	},
	{
		meta: KnownCategory{
			ID:          ToolResults,
			Name:        "Tool results",
			Description: "What a tool returned, such as command output and file contents. Success, errors and durations are kept.",
		},
		standard: agentNames{
			events:     []string{"gen_ai.tool.message"},
			attributes: []string{"gen_ai.tool.call.result"},
		},
		agents: map[string]agentNames{
			"codex": {attributes: []string{"output"}},
		},
		unknown: agentNames{
			attributes: []string{"tool_output", "tool.result"},
		},
	},
	{
		meta: KnownCategory{
			ID:          Cost,
			Name:        "Cost",
			Description: "Spend in dollars, for agents that report it. Token counts are kept.",
		},
		agents: map[string]agentNames{
			"claude-code": {metrics: []string{"claude_code.cost.usage"}, attributes: []string{"cost_usd"}},
		},
	},
	{
		meta: KnownCategory{
			ID:          Noise,
			Name:        "Noise",
			Description: "High-volume events with little value on their own. For Codex, one event per streamed chunk; the final one, which carries token usage, is kept.",
		},
		agents: map[string]agentNames{
			"codex": {dropLog: codexStreamChunk, dropLogIs: "codex.sse_event records, except event.kind = response.completed"},
		},
	},
	{
		meta: KnownCategory{
			ID:          Identity,
			Name:        "User identity",
			Description: "Who and which machine sent the telemetry: emails, user and account ids, host names. Hash it to still group by user.",
			Hashable:    true,
		},
		standard: agentNames{
			attributes: []string{"user.email", "user.id", "user.name", "enduser.id", "host.name"},
		},
		agents: map[string]agentNames{
			"claude-code": {attributes: []string{"user.account_uuid", "organization.id"}},
			"codex":       {attributes: []string{"user.account_id"}},
			"gemini-cli":  {attributes: []string{"installation.id"}},
			"cursor":      {attributes: []string{"device.id"}},
		},
	},
}

// codexStreamChunk is a codex.sse_event other than response.completed, the
// only one with token counts.
func codexStreamChunk(lr plog.LogRecord) bool {
	if !slices.Contains(eventNames(lr), "codex.sse_event") {
		return false
	}
	kind, _ := lr.Attributes().Get("event.kind")
	return kind.AsString() != "response.completed"
}

// KnownCategories lists the categories drop_known and hash_known rules accept.
var KnownCategories = func() []KnownCategory {
	out := make([]KnownCategory, 0, len(knownDefs))
	for _, d := range knownDefs {
		m := d.meta
		m.Agents = []string{}
		for agent, profile := range profiles {
			if _, ok := d.agents[profile]; ok {
				m.Agents = append(m.Agents, agent)
			}
		}
		sort.Strings(m.Agents)
		m.Standard = !d.standard.empty()
		m.Removes = []KnownRemoval{}
		if m.Standard {
			m.Removes = append(m.Removes, d.standard.removal("standard", nil))
		}
		for _, profile := range slices.Sorted(maps.Keys(d.agents)) {
			var agents []string
			for agent, p := range profiles {
				if p == profile {
					agents = append(agents, agent)
				}
			}
			sort.Strings(agents)
			m.Removes = append(m.Removes, d.agents[profile].removal("agent", agents))
		}
		if !d.unknown.empty() {
			m.Removes = append(m.Removes, d.unknown.removal("other", nil))
		}
		out = append(out, m)
	}
	return out
}()

// knownSet is what one rule does to one agent's telemetry.
type knownSet struct {
	events, attributes, metrics, bodies func(string) bool
	dropLog                             []func(plog.LogRecord) bool
	// hash replaces matching attribute values; nil removes them.
	hash func(pcommon.Value)
}

func newKnownSet(parts []agentNames, hash func(pcommon.Value)) *knownSet {
	var events, attributes, metrics, bodies []string
	var dropLog []func(plog.LogRecord) bool
	for _, n := range parts {
		attributes = append(attributes, n.attributes...)
		if hash != nil {
			continue
		}
		events = append(events, n.events...)
		metrics = append(metrics, n.metrics...)
		bodies = append(bodies, n.bodies...)
		if n.dropLog != nil {
			dropLog = append(dropLog, n.dropLog)
		}
	}
	return &knownSet{events: globs(events), attributes: globs(attributes), metrics: globs(metrics), bodies: globs(bodies), dropLog: dropLog, hash: hash}
}

// clearBody empties the body of a record whose event is in bodies.
func (s *knownSet) clearBody(lr plog.LogRecord) {
	for _, n := range eventNames(lr) {
		if s.bodies(n) {
			pcommon.NewValueEmpty().CopyTo(lr.Body())
			return
		}
	}
}

func (s *knownSet) dropRecord(lr plog.LogRecord) bool {
	for _, n := range eventNames(lr) {
		if s.events(n) {
			return true
		}
	}
	for _, drop := range s.dropLog {
		if drop(lr) {
			return true
		}
	}
	return false
}

func (s *knownSet) attrs(m pcommon.Map) {
	if s.hash == nil {
		m.RemoveIf(func(k string, _ pcommon.Value) bool { return s.attributes(k) })
		return
	}
	m.Range(func(k string, v pcommon.Value) bool {
		if s.attributes(k) {
			s.hash(v)
		}
		return true
	})
}

func compileKnown(categories []string, hash func(pcommon.Value)) (step, error) {
	if len(categories) == 0 {
		return nil, fmt.Errorf("categories are required")
	}
	var defs []knownDef
	for _, c := range categories {
		i := slices.IndexFunc(knownDefs, func(d knownDef) bool { return string(d.meta.ID) == c })
		if i < 0 {
			return nil, fmt.Errorf("unknown category %q", c)
		}
		if hash != nil && !knownDefs[i].meta.Hashable {
			return nil, fmt.Errorf("category %q cannot be hashed", c)
		}
		defs = append(defs, knownDefs[i])
	}

	byProfile := map[string]*knownSet{}
	for _, profile := range profiles {
		if _, done := byProfile[profile]; done {
			continue
		}
		var parts []agentNames
		for _, d := range defs {
			parts = append(parts, d.standard, d.agents[profile])
		}
		byProfile[profile] = newKnownSet(parts, hash)
	}
	var parts []agentNames
	for _, d := range defs {
		parts = append(parts, d.standard, d.unknown)
	}
	unknown := newKnownSet(parts, hash)

	setFor := func(resource pcommon.Resource, scope string) *knownSet {
		if profile, ok := profiles[telemetry.DetectAgent(resource, scope)]; ok {
			return byProfile[profile]
		}
		return unknown
	}
	return func(p telemetry.Payload) { applyKnown(p, setFor) }, nil
}

// applyKnown runs each scope through its agent's set. A resource is handled
// once, with the set of its first scope, so hashing never applies twice.
func applyKnown(p telemetry.Payload, setFor func(pcommon.Resource, string) *knownSet) {
	switch p.Signal {
	case telemetry.Logs:
		p.Logs.ResourceLogs().RemoveIf(func(r plog.ResourceLogs) bool {
			if r.ScopeLogs().Len() > 0 {
				defer setFor(r.Resource(), r.ScopeLogs().At(0).Scope().Name()).attrs(r.Resource().Attributes())
			}
			r.ScopeLogs().RemoveIf(func(sl plog.ScopeLogs) bool {
				s := setFor(r.Resource(), sl.Scope().Name())
				sl.LogRecords().RemoveIf(func(lr plog.LogRecord) bool {
					if s.dropRecord(lr) {
						return true
					}
					s.clearBody(lr)
					s.attrs(lr.Attributes())
					return false
				})
				return sl.LogRecords().Len() == 0
			})
			return r.ScopeLogs().Len() == 0
		})
	case telemetry.Traces:
		rs := p.Traces.ResourceSpans()
		for i := 0; i < rs.Len(); i++ {
			r := rs.At(i)
			if r.ScopeSpans().Len() == 0 {
				continue
			}
			resourceSet := setFor(r.Resource(), r.ScopeSpans().At(0).Scope().Name())
			for j := 0; j < r.ScopeSpans().Len(); j++ {
				ss := r.ScopeSpans().At(j)
				s := setFor(r.Resource(), ss.Scope().Name())
				for k := 0; k < ss.Spans().Len(); k++ {
					span := ss.Spans().At(k)
					s.attrs(span.Attributes())
					span.Events().RemoveIf(func(e ptrace.SpanEvent) bool {
						if s.events(e.Name()) {
							return true
						}
						s.attrs(e.Attributes())
						return false
					})
				}
			}
			resourceSet.attrs(r.Resource().Attributes())
		}
	case telemetry.Metrics:
		p.Metrics.ResourceMetrics().RemoveIf(func(r pmetric.ResourceMetrics) bool {
			if r.ScopeMetrics().Len() > 0 {
				defer setFor(r.Resource(), r.ScopeMetrics().At(0).Scope().Name()).attrs(r.Resource().Attributes())
			}
			r.ScopeMetrics().RemoveIf(func(sm pmetric.ScopeMetrics) bool {
				s := setFor(r.Resource(), sm.Scope().Name())
				sm.Metrics().RemoveIf(func(m pmetric.Metric) bool {
					if s.metrics(m.Name()) {
						return true
					}
					telemetry.WalkDataPoints(m, s.attrs)
					return false
				})
				return sm.Metrics().Len() == 0
			})
			return r.ScopeMetrics().Len() == 0
		})
	}
}
