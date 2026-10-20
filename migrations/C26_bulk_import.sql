-- C26: bulk CSV imports. Uploading a file stores every row with its
-- validation result (the preview); committing creates the valid rows.

CREATE TABLE bulk_import_jobs (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id bigint NOT NULL REFERENCES workspaces (id),
    owner_id     bigint NOT NULL REFERENCES users (id),
    -- preview -> committed, once: the UPDATE that commits checks the status,
    -- so two commits racing can't both create the links.
    status       text   NOT NULL DEFAULT 'preview' CHECK (status IN ('preview', 'committed')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    committed_at timestamptz
);
CREATE INDEX bulk_import_jobs_workspace_idx ON bulk_import_jobs (workspace_id);

CREATE TABLE bulk_import_rows (
    job_id      bigint NOT NULL REFERENCES bulk_import_jobs (id) ON DELETE CASCADE,
    row_no      int    NOT NULL, -- line in the file; the header is line 1
    destination text   NOT NULL DEFAULT '',
    alias       text   NOT NULL DEFAULT '',
    title       text   NOT NULL DEFAULT '',
    campaign    text   NOT NULL DEFAULT '',
    tags        text[] NOT NULL DEFAULT '{}',
    expires_at  timestamptz,
    max_clicks  bigint,
    status      text   NOT NULL CHECK (status IN ('valid', 'invalid')),
    errors      text[] NOT NULL DEFAULT '{}',
    link_id     bigint REFERENCES links (id), -- set once the row is committed
    PRIMARY KEY (job_id, row_no)
);
