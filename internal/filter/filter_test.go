package filter

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/webrix-ai/otlp-router/internal/telemetry"
)

// claudeLogs mimics Claude Code's log export: one user_prompt and one
// api_request event, each with user.email.
func claudeLogs() telemetry.Payload {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "claude-code")
	rl.Resource().Attributes().PutStr("host.name", "laptop-1")
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("com.anthropic.claude_code.events")

	prompt := sl.LogRecords().AppendEmpty()
	prompt.Body().SetStr("claude_code.user_prompt")
	prompt.Attributes().PutStr("event.name", "user_prompt")
	prompt.Attributes().PutStr("user.email", "dev@acme.com")
	prompt.Attributes().PutStr("prompt", "deploy with key sk-ant-abcdefghijklmnopqrstuvwxyz")

	api := sl.LogRecords().AppendEmpty()
	api.Body().SetStr("claude_code.api_request")
	api.Attributes().PutStr("event.name", "api_request")
	api.Attributes().PutStr("user.email", "dev@acme.com")
	api.Attributes().PutStr("model", "claude-sonnet")
	api.Attributes().PutStr("error", "Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456")
	return telemetry.FromLogs(ld)
}

func apply(t *testing.T, p telemetry.Payload, rules ...Rule) {
	t.Helper()
	c, err := Compile(rules)
	if err != nil {
		t.Fatal(err)
	}
	c.Apply(p)
}

func records(p telemetry.Payload) plog.LogRecordSlice {
	return p.Logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
}

func TestDropLogEventsMatchesAttributeOrBody(t *testing.T) {
	for _, name := range []string{"*user_prompt", "user_prompt", "claude_code.user_prompt"} {
		p := claudeLogs()
		apply(t, p, Rule{Type: DropLogEvents, Names: []string{name}})
		if got := p.Items(); got != 1 {
			t.Fatalf("pattern %q: want 1 record left, got %d", name, got)
		}
	}
}

func TestDropAllEventsPrunesResource(t *testing.T) {
	p := claudeLogs()
	apply(t, p, Rule{Type: DropLogEvents, Names: []string{"*"}})
	if !p.Empty() || p.Logs.ResourceLogs().Len() != 0 {
		t.Fatalf("want empty payload with no resources, got %d resources", p.Logs.ResourceLogs().Len())
	}
}

func TestDropAttributesByLevel(t *testing.T) {
	p := claudeLogs()
	apply(t, p, Rule{Type: DropAttributes, Keys: []string{"*.name"}, Level: "resource"})
	res := p.Logs.ResourceLogs().At(0).Resource().Attributes()
	if res.Len() != 0 {
		t.Fatalf("resource attributes should be dropped, have %v", res.AsRaw())
	}
	if _, ok := records(p).At(0).Attributes().Get("event.name"); !ok {
		t.Fatal("record-level event.name must be kept for level=resource")
	}
}

func TestMaskAndHash(t *testing.T) {
	p := claudeLogs()
	apply(t, p,
		Rule{Type: MaskAttributes, Keys: []string{"model"}},
		Rule{Type: HashAttributes, Keys: []string{"user.email"}, Salt: "s"},
	)
	api := records(p).At(1).Attributes()
	if v, _ := api.Get("model"); v.Str() != Masked {
		t.Fatalf("model not masked: %q", v.Str())
	}
	a, _ := records(p).At(0).Attributes().Get("user.email")
	b, _ := api.Get("user.email")
	if !strings.HasPrefix(a.Str(), "sha256:") || a.Str() != b.Str() {
		t.Fatalf("hash should be stable and prefixed: %q vs %q", a.Str(), b.Str())
	}
}

func TestRedactBuiltins(t *testing.T) {
	p := claudeLogs()
	apply(t, p, Rule{Type: Redact, Builtins: true})
	prompt, _ := records(p).At(0).Attributes().Get("prompt")
	if strings.Contains(prompt.Str(), "sk-ant-") {
		t.Fatalf("anthropic key not redacted: %q", prompt.Str())
	}
	errAttr, _ := records(p).At(1).Attributes().Get("error")
	if strings.Contains(errAttr.Str(), "abcdefghijklmnop") {
		t.Fatalf("bearer token not redacted: %q", errAttr.Str())
	}
}

func TestDropMetrics(t *testing.T) {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	for _, n := range []string{"claude_code.cost.usage", "claude_code.token.usage"} {
		m := sm.Metrics().AppendEmpty()
		m.SetName(n)
		m.SetEmptySum().DataPoints().AppendEmpty().SetIntValue(1)
	}
	p := telemetry.FromMetrics(md)
	apply(t, p, Rule{Type: DropMetrics, Names: []string{"*.cost.*"}})
	if p.Items() != 1 || sm.Metrics().At(0).Name() != "claude_code.token.usage" {
		t.Fatalf("want only token.usage left, got %d items", p.Items())
	}
}

func TestCompileErrors(t *testing.T) {
	cases := []Rule{
		{Type: "nope"},
		{Type: DropAttributes},
		{Type: Redact, Patterns: []string{"("}},
		{Type: DropAttributes, Keys: []string{"a"}, Level: "scope"},
	}
	for _, r := range cases {
		if _, err := Compile([]Rule{r}); err == nil {
			t.Errorf("rule %+v: expected an error", r)
		}
	}
}

func TestPresetsCompile(t *testing.T) {
	for _, p := range Presets {
		if _, err := Compile(p.Rules); err != nil {
			t.Errorf("preset %s: %v", p.ID, err)
		}
	}
}
