-- C16: campaigns, tags and folders, each private to a workspace. Names are
-- unique per workspace in any letter case, because CSV imports refer to
-- campaigns and tags by name.

CREATE TABLE campaigns (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id bigint NOT NULL REFERENCES workspaces (id),
    name         text   NOT NULL CHECK (name <> ''),
    utm_source   text   NOT NULL DEFAULT '',
    utm_medium   text   NOT NULL DEFAULT '',
    utm_campaign text   NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX campaigns_name_key ON campaigns (workspace_id, lower(name));

CREATE TABLE tags (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id bigint NOT NULL REFERENCES workspaces (id),
    name         text   NOT NULL CHECK (name <> ''),
    color        text   NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX tags_name_key ON tags (workspace_id, lower(name));

CREATE TABLE folders (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id bigint NOT NULL REFERENCES workspaces (id),
    name         text   NOT NULL CHECK (name <> ''),
    -- Deleting a folder moves its subfolders (and links) to the top level
    -- rather than deleting them.
    parent_id    bigint REFERENCES folders (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (parent_id IS NULL OR parent_id <> id)
);
CREATE INDEX folders_workspace_idx ON folders (workspace_id);

CREATE TABLE link_tags (
    link_id bigint NOT NULL REFERENCES links (id) ON DELETE CASCADE,
    tag_id  bigint NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (link_id, tag_id)
);
CREATE INDEX link_tags_tag_idx ON link_tags (tag_id);

-- Deleting a campaign or folder keeps its links; they just lose the label.
ALTER TABLE links
    ADD COLUMN campaign_id bigint REFERENCES campaigns (id) ON DELETE SET NULL,
    ADD COLUMN folder_id   bigint REFERENCES folders (id) ON DELETE SET NULL;
CREATE INDEX links_campaign_idx ON links (campaign_id) WHERE campaign_id IS NOT NULL;
CREATE INDEX links_folder_idx ON links (folder_id) WHERE folder_id IS NOT NULL;
