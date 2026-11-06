package webui

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"sync"
	"time"
)

const (
	// CodeTTL is how long a login code works.
	CodeTTL = 2 * time.Minute
	// SessionIdle ends a session that was not used for this long.
	SessionIdle = 12 * time.Hour
	// TokenTries is how many wrong Basic Auth tokens in a row are answered
	// before the token is locked for TokenLock.
	TokenTries = 5
	// TokenLock is how long Basic Auth is refused after too many wrong tokens.
	// It limits guessing to a few thousand tries a day, whoever sends them.
	TokenLock = 30 * time.Second
)

// Auth keeps the one-time login codes and the sessions. Everything is in
// memory: a restart or switching the web UI off logs everybody out.
type Auth struct {
	now   func() time.Time
	token string

	mu       sync.Mutex
	codes    map[string]time.Time // code -> expires
	sessions map[string]time.Time // session -> last used
	misses   int                  // wrong tokens in a row
	locked   time.Time            // Basic Auth is refused until then
}

// NewAuth creates an Auth. token is the optional fixed token for Basic Auth.
func NewAuth(token string, now func() time.Time) *Auth {
	if now == nil {
		now = time.Now
	}
	return &Auth{now: now, token: token, codes: map[string]time.Time{}, sessions: map[string]time.Time{}}
}

// NewCode creates a login code that works once, for CodeTTL.
func (a *Auth) NewCode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for c, exp := range a.codes {
		if !now.Before(exp) {
			delete(a.codes, c)
		}
	}
	code := rand.Text()
	a.codes[code] = now.Add(CodeTTL)
	return code
}

// Redeem exchanges a login code for a session. A code works only once, also
// when it is wrong or too late.
func (a *Auth) Redeem(code string) (session string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, found := a.codes[code]
	delete(a.codes, code)
	if !found || !a.now().Before(exp) {
		return "", false
	}
	session = rand.Text()
	a.sessions[session] = a.now()
	return session, true
}

// Valid reports whether session is a live session and extends it.
func (a *Auth) Valid(session string) bool {
	if session == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	last, ok := a.sessions[session]
	if !ok {
		return false
	}
	if a.now().Sub(last) >= SessionIdle {
		delete(a.sessions, session)
		return false
	}
	a.sessions[session] = a.now()
	return true
}

// Revoke ends a session.
func (a *Auth) Revoke(session string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, session)
}

// Reset ends all sessions and invalidates all codes.
func (a *Auth) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	clear(a.codes)
	clear(a.sessions)
}

// HasToken reports whether Basic Auth with a fixed token is set up.
func (a *Auth) HasToken() bool { return a.token != "" }

// CheckToken compares a Basic Auth password with the token in constant time.
// After TokenTries wrong tokens in a row it refuses every token, also the
// right one, for TokenLock: wait is then how long the lock still lasts.
// Sessions are not affected by the lock.
func (a *Auth) CheckToken(password string) (ok bool, wait time.Duration) {
	if a.token == "" {
		return false, 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if now.Before(a.locked) {
		return false, a.locked.Sub(now)
	}
	want, got := sha256.Sum256([]byte(a.token)), sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(want[:], got[:]) == 1 {
		a.misses = 0
		return true, 0
	}
	if a.misses++; a.misses >= TokenTries {
		a.misses = 0
		a.locked = now.Add(TokenLock)
	}
	return false, 0
}
