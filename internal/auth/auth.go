package auth

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const CookieName = "auth_token"

type Manager struct {
	secret       []byte
	secureCookie bool
	mu           sync.Mutex
	attempts     map[string]attemptState
}

type attemptState struct {
	count       int
	lockedUntil time.Time
}

func New(secret string, secureCookie bool) *Manager {
	return &Manager{secret: []byte(strings.TrimSpace(secret)), secureCookie: secureCookie, attempts: map[string]attemptState{}}
}

func (m *Manager) Configured() bool { return len(m.secret) > 0 }

func HashPassword(password string) (string, error) {
	data, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(data), err
}

func VerifyPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (m *Manager) IssueCookie(w http.ResponseWriter, r *http.Request) error {
	if !m.Configured() {
		return errors.New("JWT_SECRET not set")
	}
	exp := time.Now().Add(30 * 24 * time.Hour)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"authenticated": true, "exp": exp.Unix()})
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return err
	}
	secure := m.secureCookie || forwardedHTTPS(r) || r.TLS != nil
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: signed, Path: "/", Expires: exp, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	return nil
}

func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (m *Manager) Authenticated(r *http.Request) bool {
	if !m.Configured() {
		return false
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return false
	}
	tok, err := jwt.Parse(c.Value, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected jwt alg")
		}
		return m.secret, nil
	})
	return err == nil && tok.Valid
}

func (m *Manager) CheckGuard(ip string, enabled bool) (bool, int) {
	if !enabled {
		return true, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.attempts[ip]
	if time.Now().Before(st.lockedUntil) {
		return false, int(time.Until(st.lockedUntil).Seconds())
	}
	return true, 0
}

func (m *Manager) RecordFailure(ip string, enabled bool) (bool, int) {
	if !enabled {
		return true, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.attempts[ip]
	st.count++
	if st.count >= 5 {
		st.lockedUntil = time.Now().Add(15 * time.Minute)
		m.attempts[ip] = st
		return false, int(15 * time.Minute / time.Second)
	}
	m.attempts[ip] = st
	return true, 0
}

func (m *Manager) ClearFailures(ip string) {
	m.mu.Lock()
	delete(m.attempts, ip)
	m.mu.Unlock()
}

func forwardedHTTPS(r *http.Request) bool {
	proto := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("x-forwarded-proto"), ",")[0]))
	return proto == "https"
}
