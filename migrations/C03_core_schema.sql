-- C03: the core schema. Links and their device targets, the domain blocklist
-- and the audit log. Users, workspaces, campaigns and click events arrive in
-- later migrations, which add their foreign keys to these tables with ALTER.
-- All timestamps are UTC timestamptz.

CREATE TABLE links (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id    bigint NOT NULL,
    owner_id        bigint NOT NULL,
    code            text   NOT NULL CHECK (code <> ''),
    destination     text   NOT NULL,
    title           text   NOT NULL DEFAULT '',
    -- Expiry is not a status: it is computed from expires_at on every visit.
    status          text   NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'paused', 'archived', 'disabled_admin')),
    disabled_reason text   NOT NULL DEFAULT '',
    -- The trust columns live here, not in the trust migration, because the
    -- store and the links API read them long before the scanner exists.
    trust_state     text   NOT NULL DEFAULT 'ok'
                    CHECK (trust_state IN ('ok', 'degraded', 'changed', 'quarantined')),
    last_checked_at timestamptz,
    starts_at       timestamptz,
    expires_at      timestamptz,
    max_clicks      bigint CHECK (max_clicks > 0),
    -- Only ever changed by the atomic reservation:
    -- UPDATE links SET cap_used = cap_used + 1 WHERE id = $1 AND cap_used < $2
    cap_used        bigint NOT NULL DEFAULT 0 CHECK (cap_used >= 0),
    password_hash   bytea,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    -- Links are only soft-deleted, so their analytics survive and their code
    -- is never handed to someone else.
    deleted_at      timestamptz,
    CHECK (starts_at IS NULL OR expires_at IS NULL OR starts_at < expires_at)
);

-- Codes are case-insensitive: "Sale" and "sale" are the same link. The index
-- covers deleted links too, which is what keeps codes from being reused.
-- Creating a link inserts and retries on a violation of this index rather
-- than checking first, so two people racing for one code can't both win.
CREATE UNIQUE INDEX links_code_lower_key ON links (lower(code));
CREATE INDEX links_workspace_created_idx ON links (workspace_id, created_at DESC) WHERE deleted_at IS NULL;

-- Device targets: visits from this kind of device go to their own destination.
CREATE TABLE redirect_rules (
    link_id     bigint NOT NULL REFERENCES links (id) ON DELETE CASCADE,
    device      text   NOT NULL CHECK (device IN ('mobile', 'tablet', 'desktop')),
    destination text   NOT NULL,
    PRIMARY KEY (link_id, device)
);

-- Blocking a domain blocks its subdomains too; the matching happens in the
-- app, which keeps this small table in memory.
CREATE TABLE blocked_domains (
    domain     text PRIMARY KEY CHECK (domain <> '' AND domain = lower(domain)),
    reason     text NOT NULL DEFAULT '',
    created_by bigint,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id    bigint, -- NULL when the system acted, e.g. the trust scanner
    action      text   NOT NULL,
    entity_type text   NOT NULL,
    entity_id   bigint NOT NULL,
    detail      jsonb  NOT NULL DEFAULT '{}',
    ts          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_ts_idx ON audit_log (ts DESC, id DESC);
CREATE INDEX audit_log_entity_idx ON audit_log (entity_type, entity_id);
