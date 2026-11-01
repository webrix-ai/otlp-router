package telemetry

import (
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestNamesSkipMessagesAndKeepTheMostFrequent(t *testing.T) {
	ld := plog.NewLogs()
	lrs := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	lrs.AppendEmpty().Body().SetStr("claude_code.api_request")
	lrs.AppendEmpty().Body().SetStr("claude_code.api_request")
	lrs.AppendEmpty().Body().SetStr("please refactor the billing module")
	lrs.AppendEmpty().SetEventName("gen_ai.user.message")
	got := FromLogs(ld).Names(10)
	if len(got) != 2 || got["claude_code.api_request"] != 2 || got["gen_ai.user.message"] != 1 {
		t.Fatalf("log names: %+v", got)
	}

	td := ptrace.NewTraces()
	spans := td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans()
	for i := range 5 {
		for range i + 1 {
			spans.AppendEmpty().SetName(fmt.Sprintf("span-%d", i))
		}
	}
	got = FromTraces(td).Names(2)
	if len(got) != 2 || got["span-4"] != 5 || got["span-3"] != 4 {
		t.Fatalf("top span names: %+v", got)
	}
}
