-- C11: accounts. Every user has a workspace, and everything they create
-- belongs to that workspace. Sessions store only a SHA-256 of the token, so a
-- leaked copy of this table can't be used to log in.

CREATE TABLE workspaces (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id  bigint NOT NULL REFERENCES workspaces (id),
    email         text   NOT NULL CHECK (email <> ''),
    password_hash bytea  NOT NULL,
    name          text   NOT NULL DEFAULT '',
    role          text   NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    created_at    timestamptz NOT NULL DEFAULT now()
);
-- One account per address in any letter case. Signup inserts and lets this
-- index decide, like link codes.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

CREATE TABLE sessions (
    token_hash bytea  PRIMARY KEY, -- sha256 of the cookie value
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- links.workspace_id and links.owner_id get no foreign keys here: the C03
-- schema test inserts links for workspace 1 without creating one, and every
-- link the API creates takes both ids from the logged-in user's session.
CREATE INDEX links_owner_idx ON links (owner_id);
