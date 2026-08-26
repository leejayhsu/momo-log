package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-webauthn/webauthn/protocol"

	"momo-poo/internal/auth"
	"momo-poo/web"
)

type authUserKey struct{}

func currentUser(r *http.Request) *auth.User {
	user, _ := r.Context().Value(authUserKey{}).(*auth.User)
	if user == nil {
		return &auth.User{}
	}
	return user
}

func (a *app) protected(next http.Handler) http.Handler {
	if a.auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := a.auth.User(r)
		if err != nil {
			a.auth.ClearSessionCookie(w)
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeAPIError(w, http.StatusUnauthorized, "unauthorized", "sign in with a passkey")
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authUserKey{}, user)))
	})
}

func (a *app) loginPage(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if _, err := a.auth.User(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	renderAuthPage(w, r, web.LoginPage(a.auth.RegistrationEnabled()))
}

func (a *app) registerPage(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil || !a.auth.RegistrationEnabled() {
		http.NotFound(w, r)
		return
	}
	renderAuthPage(w, r, web.RegisterPage())
}

func renderAuthPage(w http.ResponseWriter, r *http.Request, page templ.Component) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.Render(r.Context(), w); err != nil {
		log.Printf("render auth page: %v", err)
	}
}

func (a *app) beginRegistration(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if !a.allowJSON(w, r, a.writeLimiter) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var input struct {
		Username string `json:"username"`
	}
	if err := decodeAuthJSON(r, &input); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_body", "enter a valid username")
		return
	}
	options, token, err := a.auth.BeginRegistration(r.Context(), input.Username)
	if err != nil {
		status := http.StatusInternalServerError
		code, message := "internal_error", "registration could not start"
		if errors.Is(err, auth.ErrRegistrationOff) {
			status, code, message = http.StatusForbidden, "registration_disabled", "registration is disabled"
		} else if errors.Is(err, auth.ErrInvalidUsername) {
			status, code, message = http.StatusBadRequest, "invalid_username", err.Error()
		} else if errors.Is(err, auth.ErrUsernameTaken) {
			status, code, message = http.StatusConflict, "username_taken", err.Error()
		}
		writeAPIError(w, status, code, message)
		return
	}
	a.auth.SetCeremonyCookie(w, token)
	writeJSON(w, http.StatusOK, options)
}

func (a *app) finishRegistration(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if !a.allowJSON(w, r, a.writeLimiter) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	_, sessionToken, err := a.auth.FinishRegistration(r.Context(), a.auth.CeremonyToken(r), r)
	a.auth.ClearCeremonyCookie(w)
	if err != nil {
		logPasskeyError("registration", err)
		writeAPIError(w, http.StatusBadRequest, "registration_failed", "passkey registration failed; try again")
		return
	}
	a.auth.SetSessionCookie(w, sessionToken)
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/"})
}

func (a *app) beginLogin(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if !a.allowJSON(w, r, a.readLimiter) {
		return
	}
	options, token, err := a.auth.BeginLogin()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "sign in could not start")
		return
	}
	a.auth.SetCeremonyCookie(w, token)
	writeJSON(w, http.StatusOK, options)
}

func (a *app) finishLogin(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if !a.allowJSON(w, r, a.readLimiter) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	_, sessionToken, err := a.auth.FinishLogin(r.Context(), a.auth.CeremonyToken(r), r)
	a.auth.ClearCeremonyCookie(w)
	if err != nil {
		logPasskeyError("login", err)
		writeAPIError(w, http.StatusUnauthorized, "login_failed", "passkey sign in failed; try again")
		return
	}
	a.auth.SetSessionCookie(w, sessionToken)
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/"})
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.auth.Logout(r.Context(), r); err != nil {
		log.Printf("log out: %v", err)
	}
	a.auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func logPasskeyError(ceremony string, err error) {
	var protocolError *protocol.Error
	if errors.As(err, &protocolError) && protocolError.DevInfo != "" {
		log.Printf("finish passkey %s: %v (%s)", ceremony, err, protocolError.DevInfo)
		return
	}
	log.Printf("finish passkey %s: %v", ceremony, err)
}

func decodeAuthJSON(r *http.Request, destination any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("Content-Type must be application/json")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return ensureJSONEnd(decoder)
}
