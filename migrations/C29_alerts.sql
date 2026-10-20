-- C29: alerts for link owners. At most one open alert per link and kind;
-- an alert closes when its condition clears (or, for spikes, after a while).

CREATE TABLE alerts (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    link_id      bigint NOT NULL REFERENCES links (id) ON DELETE CASCADE,
    workspace_id bigint NOT NULL,
    kind         text   NOT NULL CHECK (kind IN ('spike', 'near_cap', 'expiring', 'dead_link', 'quarantined')),
    message      text   NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    resolved_at  timestamptz
);
CREATE UNIQUE INDEX alerts_one_open_key ON alerts (link_id, kind) WHERE resolved_at IS NULL;
CREATE INDEX alerts_workspace_idx ON alerts (workspace_id, created_at DESC);
