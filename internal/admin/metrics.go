package admin

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// metrics serves Prometheus text format. It exposes counters and target ids
// only, so it is left unauthenticated for scrapers.
func (s *server) metrics(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	ingest := s.Router.IngestStats()

	b.WriteString("# HELP otlp_router_received_items_total Spans, data points or log records received.\n")
	b.WriteString("# TYPE otlp_router_received_items_total counter\n")
	for _, sig := range sortedKeys(ingest.Items) {
		fmt.Fprintf(&b, "otlp_router_received_items_total{signal=%q} %d\n", sig, ingest.Items[sig])
	}
	b.WriteString("# HELP otlp_router_received_batches_total Export requests received.\n")
	b.WriteString("# TYPE otlp_router_received_batches_total counter\n")
	for _, sig := range sortedKeys(ingest.Batches) {
		fmt.Fprintf(&b, "otlp_router_received_batches_total{signal=%q} %d\n", sig, ingest.Batches[sig])
	}

	targets := s.Router.TargetStats()
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	series := []struct {
		name, help, kind string
		value            func(id string) int64
	}{
		{"otlp_router_target_sent_items_total", "Items delivered to the target.", "counter", func(id string) int64 { return targets[id].Sent }},
		{"otlp_router_target_failed_items_total", "Items the target rejected or that failed after retries.", "counter", func(id string) int64 { return targets[id].Failed }},
		{"otlp_router_target_dropped_items_total", "Items dropped because the target queue was full.", "counter", func(id string) int64 { return targets[id].Dropped }},
		{"otlp_router_target_retries_total", "Delivery retries.", "counter", func(id string) int64 { return targets[id].Retries }},
		{"otlp_router_target_queue_length", "Batches waiting in the target queue.", "gauge", func(id string) int64 { return int64(targets[id].QueueLen) }},
	}
	for _, m := range series {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", m.name, m.help, m.name, m.kind)
		for _, id := range ids {
			fmt.Fprintf(&b, "%s{target=%q} %d\n", m.name, id, m.value(id))
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(b.String()))
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
