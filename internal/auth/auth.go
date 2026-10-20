// Package auth holds accounts and login sessions: bcrypt password hashes,
// random session tokens of which only a SHA-256 is stored, the lp_session
// cookie, and the guard that authenticates every API request.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"pathmaxing/internal/domain"
)

// CookieName is the session cookie.
const CookieName = "lp_session"

// Errors callers turn into API responses.
var (
	ErrEmailTaken     = fmt.Errorf("email is already registered: %w", domain.ErrConflict)
	ErrBadCredentials = errors.New("wrong email or password")
	ErrBadOrigin      = errors.New("request did not come from an allowed origin")
)

// User is an account as the API shows it.
type User struct {
	ID          int64       `json:"id"`
	Email       string      `json:"email"`
	Name        string      `json:"name"`
	Role        domain.Role `json:"role"`
	WorkspaceID int64       `json:"workspace_id"`
}

// Principal returns the identity the rest of the API works with.
func (u User) Principal() domain.Principal {
	return domain.Principal{UserID: u.ID, WorkspaceID: u.WorkspaceID, Role: u.Role, Email: u.Email, Name: u.Name}
}

// Options configure a Service.
type Options struct {
	TTL            time.Duration // SESSION_TTL, default 168h
	Secure         bool          // Secure cookies; true in production
	AllowedOrigins []string      // origins that may send state-changing requests
	Log            *slog.Logger
	// Cost is the bcrypt cost, default bcrypt.DefaultCost. Tests lower it,
	// because hashing at the real cost under -race takes most of a second.
	Cost int
}

// Service implements domain.AuthGuard.
type Service struct {
	pool *pgxpool.Pool
	opts Options
	// dummyHash is compared against when the email is unknown, so a login
	// takes as long whether or not the account exists.
	dummyHash []byte
}

// New returns a service over pool.
func New(pool *pgxpool.Pool, opts Options) *Service {
	if opts.TTL <= 0 {
		opts.TTL = 168 * time.Hour
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Cost == 0 {
		opts.Cost = bcrypt.DefaultCost
	}
	dummy, _ := bcrypt.GenerateFromPassword([]byte("not a real password"), opts.Cost)
	return &Service{pool: pool, opts: opts, dummyHash: dummy}
}

// HashPassword returns a bcrypt hash at the service's cost. Link passwords
// use it too, because the redirect checks them with bcrypt.
func (s *Service) HashPassword(password string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(password), s.opts.Cost)
}

// Signup creates a workspace and its first user in one transaction and logs
// the user in.
func (s *Service) Signup(ctx context.Context, email, password, name string) (User, string, error) {
	hash, err := s.HashPassword(password)
	if err != nil {
		return User{}, "", fmt.Errorf("auth: hash password: %w", err)
	}
	u, err := s.createAccount(ctx, email, name, hash, domain.RoleUser)
	if err != nil {
		return User{}, "", err
	}
	token, err := s.newSession(ctx, u.ID)
	return u, token, err
}

func (s *Service) createAccount(ctx context.Context, email, name string, hash []byte, role domain.Role) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("auth: begin: %w", err)
	}
	defer tx.Rollback(ctx) // a no-op once committed

	var wsID int64
	if err := tx.QueryRow(ctx, `INSERT INTO workspaces (name) VALUES ($1) RETURNING id`, name).Scan(&wsID); err != nil {
		return User{}, fmt.Errorf("auth: create workspace: %w", err)
	}
	u, err := scanUser(tx.QueryRow(ctx, `
		INSERT INTO users (workspace_id, email, password_hash, name, role)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, email, name, role, workspace_id`,
		wsID, email, hash, name, string(role)))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: create user: %w", err)
	}
	return u, tx.Commit(ctx)
}

// Login checks the password and starts a session. Unknown emails and wrong
// passwords give the same error, so the form can't be used to find accounts.
func (s *Service) Login(ctx context.Context, email, password string) (User, string, error) {
	var hash []byte
	u, err := scanUserWith(s.pool.QueryRow(ctx, `
		SELECT id, email, name, role, workspace_id, password_hash FROM users WHERE lower(email) = lower($1)`,
		strings.TrimSpace(email)), &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return User{}, "", ErrBadCredentials
	}
	if err != nil {
		return User{}, "", fmt.Errorf("auth: find user: %w", err)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		return User{}, "", ErrBadCredentials
	}
	// Housekeeping: this user's expired sessions are of no use to anyone.
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND expires_at <= now()`, u.ID); err != nil {
		s.opts.Log.Warn("could not delete expired sessions", "user_id", u.ID, "err", err)
	}
	token, err := s.newSession(ctx, u.ID)
	return u, token, err
}

// Logout ends the session the request carries, if any.
func (s *Service) Logout(ctx context.Context, r *http.Request) error {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(c.Value))
	if err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// Authenticate implements domain.AuthGuard: a live session, and for anything
// but GET and HEAD a request that came from one of our own origins. The
// session cookie is SameSite=Lax, and the origin check covers what Lax lets
// through (top-level POST navigations are blocked anyway, but older browsers
// and same-site subdomains are not).
func (s *Service) Authenticate(r *http.Request) (domain.Principal, error) {
	if err := s.CheckOrigin(r); err != nil {
		return domain.Principal{}, fmt.Errorf("%w: %w", domain.ErrUnauthenticated, err)
	}
	u, err := s.SessionUser(r)
	if err != nil {
		return domain.Principal{}, err
	}
	return u.Principal(), nil
}

// SessionUser returns the user whose session the request carries.
func (s *Service) SessionUser(r *http.Request) (User, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return User{}, fmt.Errorf("no session cookie: %w", domain.ErrUnauthenticated)
	}
	u, err := scanUser(s.pool.QueryRow(r.Context(), `
		SELECT u.id, u.email, u.name, u.role, u.workspace_id
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, hashToken(c.Value)))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, fmt.Errorf("session expired or unknown: %w", domain.ErrUnauthenticated)
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: load session: %w", err)
	}
	return u, nil
}

// CheckOrigin refuses state-changing requests that don't come from an
// allowed origin. Browsers send Origin on every cross-site POST; when it is
// missing, the Referer's origin is used, and with neither the request is
// refused.
func (s *Service) CheckOrigin(r *http.Request) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref, err := url.Parse(r.Referer()); err == nil && ref.Scheme != "" && ref.Host != "" {
			origin = ref.Scheme + "://" + ref.Host
		}
	}
	for _, allowed := range s.opts.AllowedOrigins {
		if origin != "" && strings.EqualFold(origin, allowed) {
			return nil
		}
	}
	return ErrBadOrigin
}

// SetCookie hands the browser its session token.
func (s *Service) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/",
		MaxAge: int(s.opts.TTL.Seconds()), Expires: time.Now().Add(s.opts.TTL),
		HttpOnly: true, Secure: s.opts.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie removes the session cookie.
func (s *Service) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.opts.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// EnsureAdmin makes sure an admin account exists for email: an existing
// user is promoted (their password is left alone), otherwise a new account
// is created with password.
func (s *Service) EnsureAdmin(ctx context.Context, email, password string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE lower(email) = lower($1)`, email)
	if err != nil {
		return fmt.Errorf("auth: promote admin: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	hash, err := s.HashPassword(password)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}
	_, err = s.createAccount(ctx, email, "Admin", hash, domain.RoleAdmin)
	if errors.Is(err, ErrEmailTaken) {
		// Another instance created it between our UPDATE and INSERT.
		_, err = s.pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE lower(email) = lower($1)`, email)
	}
	return err
}

// AccountSummary is a user as the admin users list shows them.
type AccountSummary struct {
	User
	CreatedAt time.Time `json:"created_at"`
	LinkCount int64     `json:"link_count"`
}

// ListUsers returns accounts newest first, before the id cursor.
func (s *Service) ListUsers(ctx context.Context, beforeID int64, limit int) ([]AccountSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.email, u.name, u.role, u.workspace_id, u.created_at,
		       (SELECT count(*) FROM links l WHERE l.owner_id = u.id AND l.deleted_at IS NULL)
		FROM users u WHERE ($1 = 0 OR u.id < $1) ORDER BY u.id DESC LIMIT $2`, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	defer rows.Close()
	var out []AccountSummary
	for rows.Next() {
		var a AccountSummary
		var role string
		if err := rows.Scan(&a.ID, &a.Email, &a.Name, &role, &a.WorkspaceID, &a.CreatedAt, &a.LinkCount); err != nil {
			return nil, fmt.Errorf("auth: list users: %w", err)
		}
		a.Role = domain.Role(role)
		a.CreatedAt = a.CreatedAt.UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) newSession(ctx context.Context, userID int64) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth: random token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hashToken(token), userID, time.Now().Add(s.opts.TTL).UTC())
	if err != nil {
		return "", fmt.Errorf("auth: create session: %w", err)
	}
	return token, nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func scanUser(row pgx.Row) (User, error) { return scanUserWith(row) }

func scanUserWith(row pgx.Row, extra ...any) (User, error) {
	var u User
	var role string
	dest := append([]any{&u.ID, &u.Email, &u.Name, &role, &u.WorkspaceID}, extra...)
	if err := row.Scan(dest...); err != nil {
		return User{}, err
	}
	u.Role = domain.Role(role)
	return u, nil
}
