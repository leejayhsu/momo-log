package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	ceremonyCookie = "momo_passkey_ceremony"
	sessionCookie  = "momo_session"
	ceremonyTTL    = 5 * time.Minute
	sessionTTL     = 90 * 24 * time.Hour
)

var (
	ErrInvalidUsername = errors.New("username must be 1 to 64 characters and contain no control characters")
	ErrUsernameTaken   = errors.New("username is already in use")
	ErrRegistrationOff = errors.New("registration is disabled")
	ErrInvalidCeremony = errors.New("passkey ceremony is missing or expired")
)

// Store persists users, credentials, and login sessions.
type Store interface {
	AuthUsernameExists(context.Context, string) (bool, error)
	AuthCreateUser(context.Context, *User, webauthn.Credential) error
	AuthUserByHandle(context.Context, []byte) (*User, error)
	AuthUpdateCredential(context.Context, int64, webauthn.Credential) error
	AuthCreateSession(context.Context, []byte, int64, time.Time) error
	AuthUserBySession(context.Context, []byte, time.Time) (*User, error)
	AuthDeleteSession(context.Context, []byte) error
}

// User implements webauthn.User while retaining the application's account ID.
type User struct {
	ID          int64
	Username    string
	Handle      []byte
	Credentials []webauthn.Credential
}

func (u *User) WebAuthnID() []byte                         { return u.Handle }
func (u *User) WebAuthnName() string                       { return u.Username }
func (u *User) WebAuthnDisplayName() string                { return u.Username }
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

type ceremony struct {
	kind    string
	expires time.Time
	session webauthn.SessionData
	user    *User
}

// Manager owns WebAuthn ceremonies and persistent application sessions.
type Manager struct {
	store               Store
	webauthn            *webauthn.WebAuthn
	registrationEnabled bool
	secureCookies       bool
	now                 func() time.Time
	mu                  sync.Mutex
	loginMu             sync.Mutex
	ceremonies          map[string]ceremony
}

func New(store Store, rpID, origin string, registrationEnabled bool) (*Manager, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "Momo outdoor log",
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, err
	}
	return &Manager{
		store:               store,
		webauthn:            wa,
		registrationEnabled: registrationEnabled,
		secureCookies:       strings.HasPrefix(origin, "https://"),
		now:                 time.Now,
		ceremonies:          make(map[string]ceremony),
	}, nil
}

func (m *Manager) RegistrationEnabled() bool { return m.registrationEnabled }

func (m *Manager) BeginRegistration(ctx context.Context, username string) (any, string, error) {
	if !m.registrationEnabled {
		return nil, "", ErrRegistrationOff
	}
	username = strings.TrimSpace(username)
	if !validUsername(username) {
		return nil, "", ErrInvalidUsername
	}
	exists, err := m.store.AuthUsernameExists(ctx, username)
	if err != nil {
		return nil, "", err
	}
	if exists {
		return nil, "", ErrUsernameTaken
	}
	handle, err := randomBytes(64)
	if err != nil {
		return nil, "", err
	}
	user := &User{Username: username, Handle: handle}
	creation, session, err := m.webauthn.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		}),
	)
	if err != nil {
		return nil, "", err
	}
	token, err := m.saveCeremony("register", *session, user)
	return creation, token, err
}

func (m *Manager) FinishRegistration(ctx context.Context, token string, r *http.Request) (string, string, error) {
	entry, err := m.takeCeremony(token, "register")
	if err != nil {
		return "", "", fmt.Errorf("load registration ceremony: %w", err)
	}
	credential, err := m.webauthn.FinishRegistration(entry.user, entry.session, r)
	if err != nil {
		return "", "", fmt.Errorf("verify passkey registration: %w", err)
	}
	if err := m.store.AuthCreateUser(ctx, entry.user, *credential); err != nil {
		return "", "", fmt.Errorf("save passkey user: %w", err)
	}
	sessionToken, err := m.newSession(ctx, entry.user.ID)
	if err != nil {
		return "", "", fmt.Errorf("create registration session: %w", err)
	}
	return entry.user.Username, sessionToken, err
}

func (m *Manager) BeginLogin() (any, string, error) {
	assertion, session, err := m.webauthn.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, "", err
	}
	token, err := m.saveCeremony("login", *session, nil)
	return assertion, token, err
}

func (m *Manager) FinishLogin(ctx context.Context, token string, r *http.Request) (string, string, error) {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	entry, err := m.takeCeremony(token, "login")
	if err != nil {
		return "", "", fmt.Errorf("load login ceremony: %w", err)
	}
	loaded, credential, err := m.webauthn.FinishPasskeyLogin(func(_ []byte, handle []byte) (webauthn.User, error) {
		return m.store.AuthUserByHandle(ctx, handle)
	}, entry.session, r)
	if err != nil {
		return "", "", fmt.Errorf("verify passkey login: %w", err)
	}
	user, ok := loaded.(*User)
	if !ok {
		return "", "", errors.New("unexpected WebAuthn user type")
	}
	if err := m.store.AuthUpdateCredential(ctx, user.ID, *credential); err != nil {
		return "", "", fmt.Errorf("update passkey credential: %w", err)
	}
	sessionToken, err := m.newSession(ctx, user.ID)
	if err != nil {
		return "", "", fmt.Errorf("create login session: %w", err)
	}
	return user.Username, sessionToken, err
}

func (m *Manager) User(r *http.Request) (*User, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, err
	}
	return m.store.AuthUserBySession(r.Context(), tokenHash(cookie.Value), m.now())
}

func (m *Manager) Logout(ctx context.Context, r *http.Request) error {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return m.store.AuthDeleteSession(ctx, tokenHash(cookie.Value))
}

func (m *Manager) SetCeremonyCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Value: token, Path: "/auth/", MaxAge: int(ceremonyTTL.Seconds()), HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteStrictMode})
}

func (m *Manager) CeremonyToken(r *http.Request) string {
	cookie, err := r.Cookie(ceremonyCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (m *Manager) ClearCeremonyCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Path: "/auth/", MaxAge: -1, HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteStrictMode})
}

func (m *Manager) SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteStrictMode})
}

func (m *Manager) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteStrictMode})
}

func (m *Manager) saveCeremony(kind string, session webauthn.SessionData, user *User) (string, error) {
	tokenBytes, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, item := range m.ceremonies {
		if !item.expires.After(now) {
			delete(m.ceremonies, key)
		}
	}
	m.ceremonies[token] = ceremony{kind: kind, expires: now.Add(ceremonyTTL), session: session, user: user}
	return token, nil
}

func (m *Manager) takeCeremony(token, kind string) (ceremony, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.ceremonies[token]
	delete(m.ceremonies, token)
	if !ok || entry.kind != kind || !entry.expires.After(m.now()) {
		return ceremony{}, ErrInvalidCeremony
	}
	return entry, nil
}

func (m *Manager) newSession(ctx context.Context, userID int64) (string, error) {
	tokenBytes, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	if err := m.store.AuthCreateSession(ctx, tokenHash(token), userID, m.now().Add(sessionTTL)); err != nil {
		return "", err
	}
	return token, nil
}

func tokenHash(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

func randomBytes(size int) ([]byte, error) {
	value := make([]byte, size)
	_, err := rand.Read(value)
	return value, err
}

func validUsername(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 64 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
