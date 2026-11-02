package router

import (
	"testing"

	"github.com/withwillowai/otlp-router/internal/exporter"
)

func TestLiveRingKeepsTheNewest(t *testing.T) {
	l := newLiveLog()
	first := l.add(LiveEntry{Signal: "logs"})
	for range liveCapacity {
		l.add(LiveEntry{Signal: "traces"})
	}
	l.result(first, exporter.Result{Target: "late", Status: exporter.StatusSent})

	got := l.since(0)
	if len(got) != LiveLimit {
		t.Fatalf("want %d entries, got %d", LiveLimit, len(got))
	}
	for _, e := range got {
		if e.Signal != "traces" || len(e.Targets) != 0 {
			t.Fatalf("overwritten entry came back or took a late result: %+v", e)
		}
	}
}
