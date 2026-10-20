-- C20: trust checks. Every scan of a link's destination is kept, so the
-- trust timeline can show what was seen and when. trust_state and
-- last_checked_at already exist on links (C03); this adds the scheduling and
-- the scanner's memory between checks.

CREATE TABLE destination_checks (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    link_id      bigint NOT NULL REFERENCES links (id) ON DELETE CASCADE,
    checked_at   timestamptz NOT NULL DEFAULT now(),
    destination  text   NOT NULL,           -- the destination this check was for
    http_status  int,                       -- NULL when no response arrived
    final_url    text   NOT NULL DEFAULT '',
    final_domain text   NOT NULL DEFAULT '',
    hops         int    NOT NULL DEFAULT 0,
    error        text   NOT NULL DEFAULT '',
    verdict      text   NOT NULL CHECK (verdict IN ('ok', 'failed', 'degraded', 'changed', 'quarantined')),
    sources      text[] NOT NULL DEFAULT '{}', -- the feeds and lists consulted
    detail       text   NOT NULL DEFAULT ''    -- what matched, for quarantines
);
CREATE INDEX destination_checks_link_idx ON destination_checks (link_id, checked_at DESC);

ALTER TABLE links
    ADD COLUMN next_check_at timestamptz,          -- NULL: never checked, due now
    ADD COLUMN trust_failures int NOT NULL DEFAULT 0, -- failed checks in a row
    -- The final host seen on the first good check of the current destination.
    -- Ending somewhere else later means the destination "changed".
    ADD COLUMN trust_baseline_host text NOT NULL DEFAULT '',
    ADD COLUMN trust_baseline_destination text NOT NULL DEFAULT '';

CREATE INDEX links_next_check_idx ON links (next_check_at NULLS FIRST)
    WHERE deleted_at IS NULL AND trust_state <> 'quarantined';
