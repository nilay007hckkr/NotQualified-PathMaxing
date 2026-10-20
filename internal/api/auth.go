package api

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"pathmaxing/internal/auth"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
)

const (
	minPasswordLen = 8
	// bcrypt only looks at the first 72 bytes; a longer password would
	// silently match anything with the same prefix.
	maxPasswordBytes = 72
	maxNameLen       = 100
)

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if s.Auth.CheckOrigin(r) != nil {
		httpx.WriteError(w, http.StatusForbidden, "ORIGIN_NOT_ALLOWED", "requests must come from the PathMaxing app")
		return
	}
	var in credentials
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "signup")
		return
	}
	in.Email = strings.TrimSpace(in.Email)
	in.Name = strings.TrimSpace(in.Name)
	v := newValidation()
	if addr, err := mail.ParseAddress(in.Email); err != nil || addr.Address != in.Email || !strings.Contains(in.Email[strings.LastIndexByte(in.Email, '@'):], ".") {
		v.err("email", "EMAIL_INVALID", "enter a valid email address, such as you@example.com")
	}
	switch {
	case utf8.RuneCountInString(in.Password) < minPasswordLen:
		v.err("password", "PASSWORD_TOO_SHORT", "password must be at least 8 characters")
	case len(in.Password) > maxPasswordBytes:
		v.err("password", "PASSWORD_TOO_LONG", "password must be at most 72 bytes")
	}
	if utf8.RuneCountInString(in.Name) > maxNameLen {
		v.err("name", "NAME_TOO_LONG", "name must be at most 100 characters")
	}
	if !v.ok() {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, v)
		return
	}
	u, token, err := s.Auth.Signup(r.Context(), in.Email, in.Password, in.Name)
	if errors.Is(err, auth.ErrEmailTaken) {
		httpx.WriteError(w, http.StatusConflict, "EMAIL_TAKEN", "an account with this email already exists; log in instead")
		return
	}
	if err != nil {
		s.fail(w, err, "signup")
		return
	}
	s.Auth.SetCookie(w, token)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"user": u})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.Auth.CheckOrigin(r) != nil {
		httpx.WriteError(w, http.StatusForbidden, "ORIGIN_NOT_ALLOWED", "requests must come from the PathMaxing app")
		return
	}
	var in credentials
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "login")
		return
	}
	u, token, err := s.Auth.Login(r.Context(), in.Email, in.Password)
	if errors.Is(err, auth.ErrBadCredentials) {
		httpx.WriteError(w, http.StatusUnauthorized, "BAD_CREDENTIALS", "wrong email or password")
		return
	}
	if err != nil {
		s.fail(w, err, "login")
		return
	}
	s.Auth.SetCookie(w, token)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if s.Auth.CheckOrigin(r) != nil {
		httpx.WriteError(w, http.StatusForbidden, "ORIGIN_NOT_ALLOWED", "requests must come from the PathMaxing app")
		return
	}
	if err := s.Auth.Logout(r.Context(), r); err != nil {
		s.fail(w, err, "logout")
		return
	}
	s.Auth.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, err := s.Auth.SessionUser(r)
	if errors.Is(err, domain.ErrUnauthenticated) {
		httpx.WriteError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "log in to continue")
		return
	}
	if err != nil {
		s.fail(w, err, "session")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": u})
}
