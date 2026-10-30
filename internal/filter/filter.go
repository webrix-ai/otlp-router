// Package filter applies a target's ordered rules to its copy of a payload.
package filter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/webrix-ai/otlp-router/internal/telemetry"
)

type RuleType string

const (
	DropAttributes RuleType = "drop_attributes"
	MaskAttributes RuleType = "mask_attributes"
	HashAttributes RuleType = "hash_attributes"
	DropLogEvents  RuleType = "drop_log_events"
	DropSpans      RuleType = "drop_spans"
	DropMetrics    RuleType = "drop_metrics"
	Redact         RuleType = "redact"
)

const Masked = "[REDACTED]"

// Rule is one filter step. Which fields apply depends on Type:
//   - drop/mask/hash_attributes: Keys (globs), Level, Salt (hash only)
//   - drop_log_events, drop_spans, drop_metrics: Names (globs)
//   - redact: Patterns (regexes) and/or Builtins (common secret formats)
type Rule struct {
	Type     RuleType `yaml:"type" json:"type"`
	Keys     []string `yaml:"keys,omitempty" json:"keys,omitempty"`
	Names    []string `yaml:"names,omitempty" json:"names,omitempty"`
	Patterns []string `yaml:"patterns,omitempty" json:"patterns,omitempty"`
	Builtins bool     `yaml:"builtins,omitempty" json:"builtins,omitempty"`
	// Level limits attribute rules to "resource" or "record"; empty means both.
	Level string `yaml:"level,omitempty" json:"level,omitempty"`
	Salt  string `yaml:"salt,omitempty" json:"salt,omitempty"`
}

// BuiltinSecretPatterns are applied by redact rules with Builtins set.
var BuiltinSecretPatterns = []string{
	`AKIA[0-9A-Z]{16}`,
	`(?i)bearer\s+[a-z0-9._~+/=-]{16,}`,
	`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`,
	`sk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}`,
	`gh[pousr]_[A-Za-z0-9]{36,}`,
	`xox[abprs]-[A-Za-z0-9-]{10,}`,
	`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`,
}

type step func(p telemetry.Payload)

// Chain is a compiled, reusable rule list.
type Chain struct{ steps []step }

func Compile(rules []Rule) (*Chain, error) {
	c := &Chain{}
	for i, r := range rules {
		s, err := compile(r)
		if err != nil {
			return nil, fmt.Errorf("filter %d (%s): %w", i+1, r.Type, err)
		}
		c.steps = append(c.steps, s)
	}
	return c, nil
}

func (c *Chain) Apply(p telemetry.Payload) {
	if c == nil {
		return
	}
	for _, s := range c.steps {
		s(p)
	}
}

func compile(r Rule) (step, error) {
	switch r.Type {
	case DropAttributes, MaskAttributes, HashAttributes:
		if len(r.Keys) == 0 {
			return nil, fmt.Errorf("keys are required")
		}
		match := globs(r.Keys)
		level := telemetry.Level(r.Level)
		if level != "" && level != telemetry.LevelResource && level != telemetry.LevelRecord {
			return nil, fmt.Errorf("level must be resource or record")
		}
		onMap := attributeAction(r, match)
		return func(p telemetry.Payload) {
			p.WalkAttributes(func(m pcommon.Map, l telemetry.Level) {
				if level == "" || level == l {
					onMap(m)
				}
			})
		}, nil

	case DropLogEvents:
		match, err := names(r)
		if err != nil {
			return nil, err
		}
		return func(p telemetry.Payload) {
			p.RemoveLogRecordsIf(func(lr plog.LogRecord) bool {
				for _, n := range eventNames(lr) {
					if match(n) {
						return true
					}
				}
				return false
			})
		}, nil

	case DropSpans:
		match, err := names(r)
		if err != nil {
			return nil, err
		}
		return func(p telemetry.Payload) {
			p.RemoveSpansIf(func(s ptrace.Span) bool { return match(s.Name()) })
		}, nil

	case DropMetrics:
		match, err := names(r)
		if err != nil {
			return nil, err
		}
		return func(p telemetry.Payload) {
			p.RemoveMetricsIf(func(m pmetric.Metric) bool { return match(m.Name()) })
		}, nil

	case Redact:
		patterns := append([]string{}, r.Patterns...)
		if r.Builtins {
			patterns = append(patterns, BuiltinSecretPatterns...)
		}
		if len(patterns) == 0 {
			return nil, fmt.Errorf("patterns or builtins are required")
		}
		res := make([]*regexp.Regexp, 0, len(patterns))
		for _, pat := range patterns {
			re, err := regexp.Compile(pat)
			if err != nil {
				return nil, fmt.Errorf("pattern %q: %w", pat, err)
			}
			res = append(res, re)
		}
		redact := func(s string) string {
			for _, re := range res {
				s = re.ReplaceAllString(s, Masked)
			}
			return s
		}
		return func(p telemetry.Payload) {
			p.WalkLogBodies(func(v pcommon.Value) { redactValue(v, redact) })
			p.WalkAttributes(func(m pcommon.Map, _ telemetry.Level) {
				m.Range(func(_ string, v pcommon.Value) bool {
					redactValue(v, redact)
					return true
				})
			})
		}, nil
	}
	return nil, fmt.Errorf("unknown rule type")
}

func attributeAction(r Rule, match func(string) bool) func(pcommon.Map) {
	switch r.Type {
	case DropAttributes:
		return func(m pcommon.Map) {
			m.RemoveIf(func(k string, _ pcommon.Value) bool { return match(k) })
		}
	case MaskAttributes:
		return func(m pcommon.Map) {
			m.Range(func(k string, v pcommon.Value) bool {
				if match(k) {
					v.SetStr(Masked)
				}
				return true
			})
		}
	default:
		return func(m pcommon.Map) {
			m.Range(func(k string, v pcommon.Value) bool {
				if match(k) {
					sum := sha256.Sum256([]byte(r.Salt + v.AsString()))
					v.SetStr("sha256:" + hex.EncodeToString(sum[:16]))
				}
				return true
			})
		}
	}
}

// eventNames are the places agents put a log event's name: the OTLP
// event_name field, the event.name attribute, or a short string body.
func eventNames(lr plog.LogRecord) []string {
	out := make([]string, 0, 3)
	if n := lr.EventName(); n != "" {
		out = append(out, n)
	}
	if v, ok := lr.Attributes().Get("event.name"); ok {
		out = append(out, v.AsString())
	}
	if lr.Body().Type() == pcommon.ValueTypeStr && len(lr.Body().Str()) <= 128 {
		out = append(out, lr.Body().Str())
	}
	return out
}

func redactValue(v pcommon.Value, redact func(string) string) {
	switch v.Type() {
	case pcommon.ValueTypeStr:
		if s := redact(v.Str()); s != v.Str() {
			v.SetStr(s)
		}
	case pcommon.ValueTypeMap:
		v.Map().Range(func(_ string, inner pcommon.Value) bool {
			redactValue(inner, redact)
			return true
		})
	case pcommon.ValueTypeSlice:
		for i := 0; i < v.Slice().Len(); i++ {
			redactValue(v.Slice().At(i), redact)
		}
	}
}

func names(r Rule) (func(string) bool, error) {
	if len(r.Names) == 0 {
		return nil, fmt.Errorf("names are required")
	}
	return globs(r.Names), nil
}

// globs matches any of the patterns, where * is any run of characters and ?
// one character.
func globs(patterns []string) func(string) bool {
	exact := map[string]bool{}
	var res []*regexp.Regexp
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.ContainsAny(p, "*?") {
			exact[p] = true
			continue
		}
		expr := regexp.QuoteMeta(p)
		expr = strings.ReplaceAll(expr, `\*`, ".*")
		expr = strings.ReplaceAll(expr, `\?`, ".")
		res = append(res, regexp.MustCompile("^"+expr+"$"))
	}
	return func(s string) bool {
		if exact[s] {
			return true
		}
		for _, re := range res {
			if re.MatchString(s) {
				return true
			}
		}
		return false
	}
}
