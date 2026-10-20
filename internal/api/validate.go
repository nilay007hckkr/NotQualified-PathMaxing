package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/shortcode"
	"pathmaxing/internal/urlcheck"
)

// Codes this package adds to the ones urlcheck and shortcode define.
const (
	codeThreatMatch       = "THREAT_MATCH"
	codeAliasTaken        = "ALIAS_TAKEN"
	codeExpiryInPast      = "EXPIRY_IN_PAST"
	codeExpiryBeforeStart = "EXPIRY_BEFORE_START"
	codeCapInvalid        = "CAP_INVALID"
	codeTargetDevice      = "TARGET_DEVICE_INVALID"

	warnLongChain     = "LONG_REDIRECT_CHAIN"
	warnUnreachable   = "DESTINATION_UNREACHABLE"
	warnOneClickNoPwd = "ONE_CLICK_NO_PASSWORD"

	// precheckTimeout bounds the trust layer's threat lookup and probe, so a
	// slow destination can't hold the wizard up.
	precheckTimeout = 4 * time.Second
	// longChainHops is where a redirect chain starts to hide where a link
	// really goes.
	longChainHops = 3
)

// draft is a link as it would be saved: a create's input, or a PATCH merged
// into the current link.
type draft struct {
	Destination string
	Alias       string
	Title       string
	StartsAt    *time.Time
	ExpiresAt   *time.Time
	MaxClicks   *int64
	HasPassword bool
	Targets     []domain.Target
	CampaignID  *int64
	FolderID    *int64
	TagIDs      []int64
}

// checks says which parts of a draft to check. A PATCH re-checks only what
// it changes: an untouched expiry that has since passed must not stop the
// owner from fixing a typo in the title.
type checks struct {
	destination bool
	alias       bool
	aliasTaken  bool // look the alias up now (validate); create relies on the unique index
	expiry      bool
	targets     bool
	probe       bool // reachability warnings, for the validate step only
}

var createChecks = checks{destination: true, alias: true, expiry: true, targets: true}

// fromInput builds a create draft.
func fromInput(in linkInput) (draft, []targetJSON) {
	d := draft{
		Destination: in.Destination.value(), Alias: in.Alias.value(), Title: in.Title.value(),
		StartsAt: in.StartsAt.V, ExpiresAt: in.ExpiresAt.V, MaxClicks: in.MaxClicks.V,
		HasPassword: in.Password.value() != "",
		CampaignID:  in.CampaignID.V, FolderID: in.FolderID.V, TagIDs: in.TagIDs.value(),
	}
	return d, in.Targets.value()
}

// validate checks d for p and normalizes its destinations in place.
func (s *Server) validate(ctx context.Context, p domain.Principal, d *draft, rawTargets []targetJSON, c checks) Validation {
	v := newValidation()
	opts := urlcheck.Options{SelfHosts: s.selfHosts(), Blocklist: s.Store.BlockedDomains()}

	destOK := false
	if c.destination {
		res := urlcheck.Check(d.Destination, opts)
		for _, e := range res.Errors {
			v.err("destination", e.Code, e.Message)
		}
		for _, w := range res.Warnings {
			v.warn("destination", w.Code, w.Message)
		}
		if res.OK() {
			d.Destination, destOK = res.URL, true
		}
	}

	if c.alias && d.Alias != "" {
		if code, msg := shortcode.CheckAlias(d.Alias); code != "" {
			v.err("alias", code, msg)
		} else if c.aliasTaken {
			_, err := s.Store.ResolveCode(ctx, d.Alias)
			switch {
			case err == nil:
				v.err("alias", codeAliasTaken, "alias is already taken")
			case !errors.Is(err, domain.ErrNotFound):
				s.Log.Error("alias lookup", "err", err)
			}
		}
	}

	now := s.now()
	if c.expiry && d.ExpiresAt != nil && !d.ExpiresAt.After(now) {
		v.err("expires_at", codeExpiryInPast, "expiry must be in the future")
	}
	if d.StartsAt != nil && d.ExpiresAt != nil && !d.ExpiresAt.After(*d.StartsAt) {
		v.err("expires_at", codeExpiryBeforeStart, "expiry must be after the start time")
	}
	if d.MaxClicks != nil && *d.MaxClicks < 1 {
		v.err("max_clicks", codeCapInvalid, "click limit must be at least 1")
	}

	if c.targets {
		d.Targets = d.Targets[:0]
		seen := map[domain.Device]bool{}
		for i, t := range rawTargets {
			dev := domain.Device(t.Device)
			field := fmt.Sprintf("targets[%d]", i)
			switch {
			case !dev.Valid():
				v.err(field+".device", codeTargetDevice, "device must be mobile, tablet or desktop")
			case seen[dev]:
				v.err(field+".device", codeTargetDevice, fmt.Sprintf("%s is targeted twice", dev))
			}
			seen[dev] = true
			res := urlcheck.Check(t.Destination, opts)
			for _, e := range res.Errors {
				v.err(field+".destination", e.Code, e.Message)
			}
			for _, w := range res.Warnings {
				v.warn(field+".destination", w.Code, w.Message)
			}
			d.Targets = append(d.Targets, domain.Target{Device: dev, Destination: res.URL})
		}
	}

	if d.MaxClicks != nil && *d.MaxClicks == 1 && !d.HasPassword {
		v.warn("max_clicks", warnOneClickNoPwd, "a one-click link without a password is used up by whoever opens it first, including a chat app's preview if it is mistaken for a person")
	}

	s.checkOrganisation(ctx, p, d, &v)

	if destOK {
		s.precheck(ctx, d.Destination, c.probe, &v)
	}
	return v
}

// precheck asks the trust layer about the destination. A threat-feed match
// blocks the link; the reachability probe only ever warns, and only in the
// validate step, because a site that is down now may be up at launch time.
func (s *Server) precheck(ctx context.Context, dest string, probe bool, v *Validation) {
	ctx, cancel := context.WithTimeout(ctx, precheckTimeout)
	defer cancel()
	pc, err := s.Trust.Precheck(ctx, dest)
	if err != nil {
		s.Log.Warn("trust precheck failed; publishing without it", "err", err)
		return
	}
	if pc.ThreatMatch {
		v.err("destination", codeThreatMatch, "a threat feed lists this destination as malicious")
	}
	if !probe {
		return
	}
	if pc.Reachable != nil && !*pc.Reachable {
		v.warn("destination", warnUnreachable, "the destination did not respond; check the address")
	}
	if pc.Hops >= longChainHops {
		v.warn("destination", warnLongChain, fmt.Sprintf("the destination redirects %d times before it loads", pc.Hops))
	}
}

func (s *Server) selfHosts() []string {
	u, err := url.Parse(s.BaseURL)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	return []string{u.Hostname()}
}

// checkOrganisation refuses campaign, tag and folder ids until those exist.
func (s *Server) checkOrganisation(_ context.Context, _ domain.Principal, d *draft, v *Validation) {
	if d.CampaignID != nil {
		v.err("campaign_id", "UNKNOWN_CAMPAIGN", "campaign not found")
	}
	if len(d.TagIDs) > 0 {
		v.err("tag_ids", "UNKNOWN_TAG", "tag not found")
	}
	if d.FolderID != nil {
		v.err("folder_id", "UNKNOWN_FOLDER", "folder not found")
	}
}
