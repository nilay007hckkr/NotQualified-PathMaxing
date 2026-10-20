-- C09: click events. Every visit, allowed or refused, is one row with its
-- outcome and traffic class, written asynchronously in batches.
-- No raw IP is ever stored: visitor_hash is a daily-rotating HMAC of it.

CREATE TABLE click_events (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Made by the redirect handler for each visit. A batch that is retried
    -- after a timeout (it may have committed after all) inserts nothing twice.
    event_id      uuid   NOT NULL UNIQUE,
    link_id       bigint NOT NULL REFERENCES links (id),
    workspace_id  bigint NOT NULL,
    ts            timestamptz NOT NULL,
    outcome       text   NOT NULL,
    traffic_class text   NOT NULL,
    class_reason  text   NOT NULL DEFAULT '',
    source        text   NOT NULL DEFAULT 'link',
    method        text   NOT NULL DEFAULT 'GET',
    device        text   NOT NULL DEFAULT '',
    browser       text   NOT NULL DEFAULT '',
    os            text   NOT NULL DEFAULT '',
    referrer_host text   NOT NULL DEFAULT '',
    country       text   NOT NULL DEFAULT '',
    visitor_hash  text   NOT NULL DEFAULT ''
);

-- Analytics always filter by link or workspace and a time range.
CREATE INDEX click_events_link_ts_idx ON click_events (link_id, ts);
CREATE INDEX click_events_workspace_ts_idx ON click_events (workspace_id, ts);
