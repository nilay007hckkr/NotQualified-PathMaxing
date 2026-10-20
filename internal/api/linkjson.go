package api

import (
	"encoding/json"
	"time"

	"pathmaxing/internal/domain"
)

// opt is a JSON field that remembers whether it was sent at all, so a PATCH
// can tell "leave it" (absent) from "clear it" (null).
type opt[T any] struct {
	Set bool
	V   *T
}

// UnmarshalJSON is only called when the field is present, null included.
func (o *opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.V = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.V = &v
	return nil
}

// value returns the field's value, or the zero value when it is absent or null.
func (o opt[T]) value() T {
	var zero T
	if o.V == nil {
		return zero
	}
	return *o.V
}

type targetJSON struct {
	Device      string `json:"device"`
	Destination string `json:"destination"`
}

// linkInput is the link input from API.md. Every field is optional so the
// same type serves create and PATCH.
type linkInput struct {
	Destination opt[string]       `json:"destination"`
	Alias       opt[string]       `json:"alias"`
	Title       opt[string]       `json:"title"`
	StartsAt    opt[time.Time]    `json:"starts_at"`
	ExpiresAt   opt[time.Time]    `json:"expires_at"`
	MaxClicks   opt[int64]        `json:"max_clicks"`
	Password    opt[string]       `json:"password"`
	Targets     opt[[]targetJSON] `json:"targets"`
	CampaignID  opt[int64]        `json:"campaign_id"`
	TagIDs      opt[[]int64]      `json:"tag_ids"`
	FolderID    opt[int64]        `json:"folder_id"`
}

// linkJSON is the link object from API.md.
type linkJSON struct {
	ID            int64        `json:"id"`
	Code          string       `json:"code"`
	ShortURL      string       `json:"short_url"`
	Destination   string       `json:"destination"`
	Title         string       `json:"title"`
	Status        string       `json:"status"`
	TrustState    string       `json:"trust_state"`
	StartsAt      *time.Time   `json:"starts_at"`
	ExpiresAt     *time.Time   `json:"expires_at"`
	IsExpired     bool         `json:"is_expired"`
	MaxClicks     *int64       `json:"max_clicks"`
	CapUsed       int64        `json:"cap_used"`
	HasPassword   bool         `json:"has_password"`
	Targets       []targetJSON `json:"targets"`
	CampaignID    *int64       `json:"campaign_id"`
	TagIDs        []int64      `json:"tag_ids"`
	FolderID      *int64       `json:"folder_id"`
	LastCheckedAt *time.Time   `json:"last_checked_at"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

func (s *Server) linkJSON(l domain.Link) linkJSON {
	out := linkJSON{
		ID: l.ID, Code: l.Code, ShortURL: s.BaseURL + "/" + l.Code,
		Destination: l.Destination, Title: l.Title,
		Status: string(l.Status), TrustState: string(l.TrustState),
		StartsAt: utc(l.StartsAt), ExpiresAt: utc(l.ExpiresAt), IsExpired: l.IsExpired(s.now()),
		MaxClicks: l.MaxClicks, CapUsed: l.CapUsed, HasPassword: l.HasPassword(),
		Targets:    []targetJSON{},
		CampaignID: l.CampaignID, TagIDs: l.TagIDs, FolderID: l.FolderID,
		LastCheckedAt: utc(l.LastCheckedAt), CreatedAt: l.CreatedAt.UTC(), UpdatedAt: l.UpdatedAt.UTC(),
	}
	for _, t := range l.Targets {
		out.Targets = append(out.Targets, targetJSON{Device: string(t.Device), Destination: t.Destination})
	}
	if out.TagIDs == nil {
		out.TagIDs = []int64{}
	}
	return out
}

// utc makes timestamps come out in UTC whatever the server's time zone is.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
