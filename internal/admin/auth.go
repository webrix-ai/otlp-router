package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/webrix-ai/otlp-router/internal/config"
)

const (
	sessionCookie = "otlp_router_session"
	sessionTTL    = 12 * time.Hour
	loginWindow   = 15 * time.Minute
	loginAttempts = 10
)

type auth struct {
	username string
	hash     []byte
	secret   []byte

	mu       sync.Mutex
	failures map[string][]time.Time
}

func newAuth(cfg config.Auth) (*auth, error) {
	a := &auth{username: cfg.Admin.Username, failures: map[string][]time.Time{}}
	if cfg.Admin.PasswordHash != "" {
		a.hash = []byte(cfg.Admin.PasswordHash)
	} else {
		h, err := bcrypt.GenerateFromPassword([]byte(cfg.Admin.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		a.hash = h
	}
	if cfg.SessionSecret != "" {
		a.secret = []byte(cfg.SessionSecret)
	} else {
		// Sessions then end on restart and are not shared between replicas.
		a.secret = make([]byte, 32)
		if _, err := rand.Read(a.secret); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func (a *auth) check(username, password string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1
	passOK := bcrypt.CompareHashAndPassword(a.hash, []byte(password)) == nil
	return userOK && passOK
}

// allowAttempt rate-limits failed logins per client IP.
func (a *auth) allowAttempt(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := time.Now().Add(-loginWindow)
	recent := a.failures[ip][:0]
	for _, t := range a.failures[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	a.failures[ip] = recent
	return len(recent) < loginAttempts
}

func (a *auth) recordFailure(ip string) {
	a.mu.Lock()
	a.failures[ip] = append(a.failures[ip], time.Now())
	a.mu.Unlock()
}

func (a *auth) sign(payload string) string {
	m := hmac.New(sha256.New, a.secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (a *auth) issue(w http.ResponseWriter, r *http.Request) {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%s|%d", a.username, exp)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + a.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(exp, 0),
	})
}

func (a *auth) clear(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

// session returns the logged-in username, or "".
func (a *auth) session(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	enc, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return ""
	}
	payload := string(raw)
	if !hmac.Equal([]byte(sig), []byte(a.sign(payload))) {
		return ""
	}
	user, expStr, ok := strings.Cut(payload, "|")
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if !ok || err != nil || time.Now().Unix() > exp || user != a.username {
		return ""
	}
	return user
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
