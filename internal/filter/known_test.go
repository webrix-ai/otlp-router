package filter

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/withwillowai/otlp-router/internal/telemetry"
)

// event appends a log record named like the agent names its events.
func event(sl plog.ScopeLogs, name string, attrs map[string]string) {
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("event.name", name)
	for k, v := range attrs {
		lr.Attributes().PutStr(k, v)
	}
}

func agentLogs(ld plog.Logs, service string) plog.ScopeLogs {
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", service)
	return rl.ScopeLogs().AppendEmpty()
}

func known(t *testing.T, p telemetry.Payload, categories ...Category) {
	t.Helper()
	var cs []string
	for _, c := range categories {
		cs = append(cs, string(c))
	}
	apply(t, p, Rule{Type: DropKnown, Categories: cs})
}

func has(lr plog.LogRecord, key string) bool {
	_, ok := lr.Attributes().Get(key)
	return ok
}

func TestDropKnownPromptsKeepsTheEvent(t *testing.T) {
	ld := plog.NewLogs()
	sl := agentLogs(ld, "claude-code")
	event(sl, "user_prompt", map[string]string{"prompt": "fix the build", "prompt_length": "13"})
	p := telemetry.FromLogs(ld)

	known(t, p, Prompts)

	if p.Items() != 1 {
		t.Fatalf("the prompt event should be kept, got %d records", p.Items())
	}
	lr := sl.LogRecords().At(0)
	if has(lr, "prompt") || !has(lr, "prompt_length") {
		t.Fatalf("want prompt text removed and its length kept, got %v", lr.Attributes().AsRaw())
	}
}

func codexToolResult() (telemetry.Payload, plog.LogRecord, plog.LogRecord) {
	ld := plog.NewLogs()
	codex := agentLogs(ld, "codex")
	event(codex, "codex.tool_result", map[string]string{"tool_name": "shell", "arguments": "cat .env", "output": "SECRET=1"})
	other := agentLogs(ld, "my-agent")
	event(other, "build", map[string]string{"output": "ok"})
	return telemetry.FromLogs(ld), codex.LogRecords().At(0), other.LogRecords().At(0)
}

func TestDropKnownToolArgumentsKeepsResults(t *testing.T) {
	p, codex, _ := codexToolResult()

	known(t, p, ToolArguments)

	if has(codex, "arguments") || !has(codex, "output") || !has(codex, "tool_name") {
		t.Fatalf("want arguments removed, output and tool name kept, got %v", codex.Attributes().AsRaw())
	}
}

func TestDropKnownToolResultsKeepsArguments(t *testing.T) {
	p, codex, other := codexToolResult()

	known(t, p, ToolResults)

	if has(codex, "output") || !has(codex, "arguments") {
		t.Fatalf("want output removed and arguments kept, got %v", codex.Attributes().AsRaw())
	}
	if !has(other, "output") {
		t.Fatal("output is Codex's name for tool results; another agent's output attribute must be kept")
	}
}

func TestDropKnownSharedProfiles(t *testing.T) {
	ld := plog.NewLogs()
	qwen := agentLogs(ld, "qwen-code")
	event(qwen, "qwen-code.tool_call", map[string]string{"function_args": `{"path":"/etc/passwd"}`})
	p := telemetry.FromLogs(ld)

	known(t, p, ToolArguments)

	if has(qwen.LogRecords().At(0), "function_args") {
		t.Fatal("Qwen Code uses Gemini CLI's names and should be filtered like it")
	}
}

func TestDropKnownPromptsUnknownAgentFallsBackToDroppingEvents(t *testing.T) {
	ld := plog.NewLogs()
	sl := agentLogs(ld, "my-agent")
	event(sl, "my_agent.user_prompt", map[string]string{"text": "hello"})
	event(sl, "my_agent.api_request", nil)
	p := telemetry.FromLogs(ld)

	known(t, p, Prompts)

	if p.Items() != 1 || sl.LogRecords().At(0).Attributes().AsRaw()["event.name"] != "my_agent.api_request" {
		t.Fatalf("an unprofiled agent's prompt event should be dropped whole, got %d records", p.Items())
	}
}

func TestDropKnownGenAISpans(t *testing.T) {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "copilot-chat")
	span := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetName("chat gpt-5")
	span.Attributes().PutStr("gen_ai.request.model", "gpt-5")
	span.Attributes().PutStr("gen_ai.input.messages", `[{"role":"user"}]`)
	span.Attributes().PutStr("gen_ai.output.messages", `[{"role":"assistant"}]`)
	span.Attributes().PutStr("gen_ai.system_instructions", "You are a coding agent")
	span.Events().AppendEmpty().SetName("gen_ai.user.message")
	span.Events().AppendEmpty().SetName("gen_ai.system.message")
	p := telemetry.FromTraces(td)

	known(t, p, Prompts)

	got := span.Attributes().AsRaw()
	if _, ok := got["gen_ai.input.messages"]; ok {
		t.Fatalf("input messages should be removed, got %v", got)
	}
	for _, key := range []string{"gen_ai.output.messages", "gen_ai.system_instructions"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("%s belongs to another category and must stay", key)
		}
	}
	if span.Events().Len() != 1 || span.Events().At(0).Name() != "gen_ai.system.message" {
		t.Fatalf("want only the user message event removed, have %d events", span.Events().Len())
	}

	known(t, p, SystemPrompts)

	if _, ok := span.Attributes().Get("gen_ai.system_instructions"); ok || span.Events().Len() != 0 {
		t.Fatal("system prompts should remove the instructions and the system message event")
	}
}

func TestDropKnownCost(t *testing.T) {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", "claude-code")
	sm := rm.ScopeMetrics().AppendEmpty()
	for _, n := range []string{"claude_code.cost.usage", "claude_code.token.usage"} {
		m := sm.Metrics().AppendEmpty()
		m.SetName(n)
		m.SetEmptySum().DataPoints().AppendEmpty().SetIntValue(1)
	}
	p := telemetry.FromMetrics(md)

	known(t, p, Cost)

	if sm.Metrics().Len() != 1 || sm.Metrics().At(0).Name() != "claude_code.token.usage" {
		t.Fatalf("want only token usage left, got %d metrics", sm.Metrics().Len())
	}
}

func TestDropKnownCodexNoiseKeepsTokenUsage(t *testing.T) {
	ld := plog.NewLogs()
	sl := agentLogs(ld, "codex")
	for _, kind := range []string{"response.created", "response.output_text.delta", "response.completed"} {
		event(sl, "codex.sse_event", map[string]string{"event.kind": kind})
	}
	event(sl, "codex.tool_decision", nil)
	p := telemetry.FromLogs(ld)

	known(t, p, Noise)

	var kept []string
	for i := 0; i < sl.LogRecords().Len(); i++ {
		a := sl.LogRecords().At(i).Attributes().AsRaw()
		kept = append(kept, a["event.name"].(string)+":"+asString(a["event.kind"]))
	}
	want := []string{"codex.sse_event:response.completed", "codex.tool_decision:"}
	if !slices.Equal(kept, want) {
		t.Fatalf("kept %v, want %v", kept, want)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func codexIdentityLogs() (telemetry.Payload, plog.ResourceLogs) {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "codex")
	rl.Resource().Attributes().PutStr("host.name", "laptop-1")
	sl := rl.ScopeLogs().AppendEmpty()
	event(sl, "codex.user_prompt", map[string]string{"user.email": "dev@acme.com", "user.account_id": "acct-1"})
	event(sl, "codex.tool_decision", map[string]string{"user.email": "dev@acme.com"})
	return telemetry.FromLogs(ld), rl
}

func TestHashKnownIdentity(t *testing.T) {
	p, rl := codexIdentityLogs()

	apply(t, p, Rule{Type: HashKnown, Categories: []string{string(Identity)}, Salt: "s"})

	recs := rl.ScopeLogs().At(0).LogRecords()
	a := recs.At(0).Attributes().AsRaw()
	b := recs.At(1).Attributes().AsRaw()
	for _, v := range []any{a["user.email"], a["user.account_id"], rl.Resource().Attributes().AsRaw()["host.name"]} {
		if s, _ := v.(string); !strings.HasPrefix(s, "sha256:") {
			t.Fatalf("want hashed identity, got %v", v)
		}
	}
	if a["user.email"] != b["user.email"] {
		t.Fatal("the same email must hash to the same value, so it can still be grouped")
	}
	if a["event.name"] != "codex.user_prompt" {
		t.Fatal("hashing identity must not touch other attributes")
	}
	host := rl.Resource().Attributes().AsRaw()["host.name"]
	if again := hashValue("s", pcommon.NewValueStr("laptop-1")); host != again {
		t.Fatalf("a resource attribute must be hashed exactly once, got %v want %v", host, again)
	}
}

func TestDropKnownIdentity(t *testing.T) {
	p, rl := codexIdentityLogs()

	known(t, p, Identity)

	if _, ok := rl.Resource().Attributes().Get("host.name"); ok {
		t.Fatal("host.name on the resource should be removed")
	}
	if a := rl.ScopeLogs().At(0).LogRecords().At(0).Attributes().AsRaw(); a["user.account_id"] != nil || a["user.email"] != nil {
		t.Fatalf("identity attributes should be removed, got %v", a)
	}
	if _, ok := rl.Resource().Attributes().Get("service.name"); !ok {
		t.Fatal("service.name is not identity and must stay")
	}
}

func TestRedactSecretsByID(t *testing.T) {
	p := claudeLogs()

	apply(t, p, Rule{Type: Redact, Secrets: []string{"bearer_token"}})

	prompt, _ := records(p).At(0).Attributes().Get("prompt")
	if !strings.Contains(prompt.Str(), "sk-ant-") {
		t.Fatal("only bearer tokens were selected; the Anthropic key must stay")
	}
	errAttr, _ := records(p).At(1).Attributes().Get("error")
	if strings.Contains(errAttr.Str(), "abcdefghijklmnop") {
		t.Fatalf("bearer token not redacted: %q", errAttr.Str())
	}
}

func TestSecretPatternsCompile(t *testing.T) {
	for _, s := range SecretPatterns {
		if _, err := Compile([]Rule{{Type: Redact, Secrets: []string{s.ID}}}); err != nil {
			t.Errorf("%s: %v", s.ID, err)
		}
	}
}

func TestDropKnownCompileErrors(t *testing.T) {
	for _, r := range []Rule{
		{Type: DropKnown},
		{Type: DropKnown, Categories: []string{"prompts", "secrets"}},
		{Type: HashKnown, Categories: []string{"prompts"}},
		{Type: Redact, Secrets: []string{"nope"}},
	} {
		if _, err := Compile([]Rule{r}); err == nil {
			t.Errorf("rule %+v: expected an error", r)
		}
	}
}

func TestKnownCategoriesDescribeWhatTheyRemove(t *testing.T) {
	removes := map[Category][]KnownRemoval{}
	for _, c := range KnownCategories {
		removes[c.ID] = c.Removes
	}
	find := func(c Category, source, agent string) KnownRemoval {
		for _, r := range removes[c] {
			if r.Source == source && (agent == "" || slices.Contains(r.Agents, agent)) {
				return r
			}
		}
		t.Fatalf("%s has no %s removal for %q", c, source, agent)
		return KnownRemoval{}
	}

	if r := find(ToolResults, "agent", "codex"); !slices.Equal(r.Attributes, []string{"output"}) {
		t.Errorf("Codex tool results should be output, got %v", r.Attributes)
	}
	if r := find(ToolArguments, "agent", "qwen-code"); !slices.Equal(r.Agents, []string{"gemini-cli", "qwen-code"}) {
		t.Errorf("Gemini CLI's names should list the agents sharing them, got %v", r.Agents)
	}
	if r := find(Noise, "agent", "codex"); len(r.Rules) != 1 {
		t.Errorf("Codex noise should be described as a rule, got %+v", r)
	}
	find(Prompts, "standard", "")
	find(Prompts, "other", "")
}

func TestKnownCategoriesSerializeListsNotNull(t *testing.T) {
	raw, err := json.Marshal(KnownCategories)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		for _, key := range []string{"agents", "removes"} {
			if _, ok := c[key].([]any); !ok {
				t.Errorf("%s: %s must be a list for the UI, got %v", c["id"], key, c[key])
			}
		}
	}
}

func TestKnownCategoriesListAgents(t *testing.T) {
	for _, c := range KnownCategories {
		if c.ID != Prompts {
			continue
		}
		for _, a := range []string{"claude-code", "claude-cowork", "codex", "gemini-cli", "qwen-code"} {
			if !slices.Contains(c.Agents, a) {
				t.Errorf("prompts should list %s, have %v", a, c.Agents)
			}
		}
		if !c.Standard {
			t.Error("prompts should cover OpenTelemetry standard names")
		}
		return
	}
	t.Fatal("prompts category missing")
}
