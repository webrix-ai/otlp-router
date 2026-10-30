package admin

import (
	"errors"
	"net/http"
	"time"

	"github.com/webrix-ai/otlp-router/internal/config"
	"github.com/webrix-ai/otlp-router/internal/router"
)

type tokenView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Hint       string `json:"hint"`
	CreatedAt  int64  `json:"createdAt"`
	ExpiresAt  int64  `json:"expiresAt,omitempty"`
	LastUsedAt int64  `json:"lastUsedAt,omitempty"`
}

func viewToken(t config.IngestToken, lastUsed map[string]int64) tokenView {
	v := tokenView{ID: t.ID, Name: t.Name, Hint: t.Hint, CreatedAt: t.CreatedAt.UnixMilli(), LastUsedAt: lastUsed[t.ID]}
	if t.ExpiresAt != nil {
		v.ExpiresAt = t.ExpiresAt.UnixMilli()
	}
	return v
}

func (s *server) tokensResponse() map[string]any {
	info := s.Router.IngestAuth()
	now := time.Now()
	views := []tokenView{}
	for _, t := range info.Tokens {
		if !t.Expired(now) {
			views = append(views, viewToken(t, info.LastUsed))
		}
	}
	return map[string]any{
		"tokens":       views,
		"configTokens": info.ConfigTokens,
		"open":         info.Open,
		"rejected":     info.Rejected,
		"version":      info.Version,
	}
}

func (s *server) listTokens(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.tokensResponse())
}

func (s *server) createToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret, tok, err := s.Router.CreateIngestToken(r.Context(), req.Name)
	s.tokenResult(w, "ingest token created", secret, tok, err)
}

func (s *server) rotateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GraceSeconds int64 `json:"graceSeconds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret, tok, err := s.Router.RotateIngestToken(r.Context(), r.PathValue("id"), time.Duration(req.GraceSeconds)*time.Second)
	s.tokenResult(w, "ingest token rotated", secret, tok, err)
}

func (s *server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Router.RevokeIngestToken(r.Context(), id); err != nil {
		s.tokenError(w, err)
		return
	}
	s.Log.Info("ingest token revoked", "id", id)
	writeJSON(w, http.StatusOK, s.tokensResponse())
}

func (s *server) tokenResult(w http.ResponseWriter, msg, secret string, tok config.IngestToken, err error) {
	if err != nil {
		s.tokenError(w, err)
		return
	}
	s.Log.Info(msg, "id", tok.ID, "name", tok.Name)
	out := s.tokensResponse()
	out["created"] = viewToken(tok, nil)
	out["secret"] = secret
	writeJSON(w, http.StatusOK, out)
}

func (s *server) tokenError(w http.ResponseWriter, err error) {
	if errors.Is(err, router.ErrTokenNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}
