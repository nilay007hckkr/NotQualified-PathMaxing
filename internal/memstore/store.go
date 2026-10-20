// Package memstore is the in-memory default for the link store, the
// blocklist and the audit log, plus a click sink that keeps recent events.
// The server runs on it until the Postgres modules replace it, and the
// redirect, rule and cache tests use it as a fast but real implementation:
// it follows the same rules as the database (case-insensitive unique codes,
// a cap that concurrent callers can never overshoot).
package memstore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"pathmaxing/internal/domain"
)

// Store keeps links, blocked domains and audit entries in memory.
type Store struct {
	mu      sync.Mutex
	links   map[int64]domain.Link
	byCode  map[string]int64 // normalized code -> link ID
	nextID  int64
	blocked map[string]string // blocked domain -> reason
	audit   []domain.AuditEntry
	now     func() time.Time
}

// New returns an empty store.
func New() *Store {
	return &Store{
		links:   make(map[int64]domain.Link),
		byCode:  make(map[string]int64),
		blocked: make(map[string]string),
		now:     time.Now,
	}
}

// Put stores l and returns it with its ID and timestamps filled in. A link
// with ID 0 is added; otherwise the link with that ID is replaced. Like the
// unique index on lower(code), Put refuses a code that another link already
// uses in any letter case, and returns domain.ErrConflict.
func (s *Store) Put(l domain.Link) (domain.Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := domain.NormalizeCode(l.Code)
	if key == "" {
		return domain.Link{}, fmt.Errorf("memstore: link has no code")
	}
	if id, taken := s.byCode[key]; taken && id != l.ID {
		return domain.Link{}, fmt.Errorf("memstore: code %q: %w", l.Code, domain.ErrConflict)
	}
	now := s.now().UTC()
	if l.ID == 0 {
		s.nextID++
		l.ID = s.nextID
		l.CreatedAt = now
	} else if old, ok := s.links[l.ID]; ok {
		delete(s.byCode, domain.NormalizeCode(old.Code))
	}
	if l.Status == "" {
		l.Status = domain.StatusActive
	}
	if l.TrustState == "" {
		l.TrustState = domain.TrustOK
	}
	l.UpdatedAt = now
	s.links[l.ID] = l.Clone()
	s.byCode[key] = l.ID
	return l.Clone(), nil
}

// ResolveCode implements domain.LinkResolver.
func (s *Store) ResolveCode(_ context.Context, code string) (domain.Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byCode[domain.NormalizeCode(code)]
	if !ok {
		return domain.Link{}, fmt.Errorf("code %q: %w", code, domain.ErrNotFound)
	}
	return s.links[id].Clone(), nil
}

// GetLink implements domain.LinkStore.
func (s *Store) GetLink(_ context.Context, id int64) (domain.Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.links[id]
	if !ok {
		return domain.Link{}, fmt.Errorf("link %d: %w", id, domain.ErrNotFound)
	}
	return l.Clone(), nil
}

// ReserveClick implements domain.CapReserver with the same rule as the
// Postgres statement: add one only while cap_used < max.
func (s *Store) ReserveClick(_ context.Context, linkID, max int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.links[linkID]
	if !ok {
		return false, fmt.Errorf("link %d: %w", linkID, domain.ErrNotFound)
	}
	if l.CapUsed >= max {
		return false, nil
	}
	l.CapUsed++
	s.links[linkID] = l
	return true, nil
}

// BlockDomain adds d (and so all of its subdomains) to the blocklist.
func (s *Store) BlockDomain(d, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocked[normalizeHost(d)] = reason
}

// UnblockDomain removes d from the blocklist.
func (s *Store) UnblockDomain(d string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blocked, normalizeHost(d))
}

// Blocked implements domain.Blocklist. It walks up host's labels, so
// blocking example.com also blocks a.b.example.com but not badexample.com.
func (s *Store) Blocked(_ context.Context, host string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := normalizeHost(host)
	for h != "" {
		if _, ok := s.blocked[h]; ok {
			return h, true, nil
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	return "", false, nil
}

// Append implements domain.AuditLog.
func (s *Store) Append(_ context.Context, e domain.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.At.IsZero() {
		e.At = s.now().UTC()
	}
	s.audit = append(s.audit, e)
	return nil
}

// AuditEntries returns a copy of the audit log, oldest first.
func (s *Store) AuditEntries() []domain.AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.AuditEntry(nil), s.audit...)
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}
