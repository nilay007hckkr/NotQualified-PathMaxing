-- C24: link-in-bio pages. A page lists some of a workspace's links, in the
-- owner's order, at /bio/{slug}. Slugs follow the alias rules and are
-- unique in any letter case.

CREATE TABLE bio_pages (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id bigint NOT NULL REFERENCES workspaces (id),
    slug         text   NOT NULL CHECK (slug <> ''),
    title        text   NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX bio_pages_slug_lower_key ON bio_pages (lower(slug));
CREATE INDEX bio_pages_workspace_idx ON bio_pages (workspace_id);

CREATE TABLE bio_page_links (
    bio_page_id bigint NOT NULL REFERENCES bio_pages (id) ON DELETE CASCADE,
    link_id     bigint NOT NULL REFERENCES links (id),
    position    int    NOT NULL,
    PRIMARY KEY (bio_page_id, link_id)
);
