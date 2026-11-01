package admin

import (
	"net/http"
	"strconv"

	"github.com/webrix-ai/otlp-router/internal/router"
)

type liveResponse struct {
	Entries []router.LiveEntry `json:"entries"`
	Pods    *podsReport        `json:"pods,omitempty"`
}

func liveSince(r *http.Request) int64 {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	return since
}

// internalLive serves this replica's recent requests to the other replicas.
func (s *server) internalLive(w http.ResponseWriter, r *http.Request) {
	if !s.isPeer(r) || !s.Router.LiveEnabled() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, liveResponse{Entries: s.Router.Live(liveSince(r))})
}

// live returns the newest requests changed after ?since= (unix ms) on every
// replica, newest first.
func (s *server) live(w http.ResponseWriter, r *http.Request) {
	if !s.Router.LiveEnabled() {
		writeError(w, http.StatusNotFound, "the live view is turned off for this router")
		return
	}
	since := liveSince(r)
	if s.Config.Peers == "" {
		writeJSON(w, http.StatusOK, liveResponse{Entries: s.Router.Live(since)})
		return
	}
	got, pods := fanOut[liveResponse](r.Context(), s, "/internal/live?since="+strconv.FormatInt(since, 10))
	if len(got) == 0 {
		writeJSON(w, http.StatusOK, liveResponse{Entries: s.Router.Live(since), Pods: &pods})
		return
	}
	var entries []router.LiveEntry
	for _, p := range got {
		entries = append(entries, p.Entries...)
	}
	if entries == nil {
		entries = []router.LiveEntry{}
	}
	writeJSON(w, http.StatusOK, liveResponse{Entries: router.NewestLive(entries), Pods: &pods})
}
