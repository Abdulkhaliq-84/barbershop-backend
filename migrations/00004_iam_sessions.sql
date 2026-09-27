-- iam: sessions and refresh tokens (ADR-0007, ADR-0014).
-- A session is one signed-in device. Its refresh tokens form a chain: each
-- refresh marks the presented token used and adds the next one. Presenting a
-- used token again means it was copied, and the whole session is revoked.

-- +goose Up
CREATE TABLE iam.sessions (
    id                uuid PRIMARY KEY,               -- UUIDv7, generated in Go
    user_id           uuid        NOT NULL REFERENCES iam.users (id),
    created_at        timestamptz NOT NULL,
    last_refreshed_at timestamptz NOT NULL,
    revoked_at        timestamptz,
    revoke_reason     text CHECK (revoke_reason IN ('logout', 'reuse', 'blocked')),
    CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);

CREATE INDEX sessions_user_id_idx ON iam.sessions (user_id);

-- Only a SHA-256 hash of each refresh token is stored: the tokens are 256-bit
-- random values, so a leaked table can't be turned back into usable tokens.
CREATE TABLE iam.refresh_tokens (
    token_hash bytea       PRIMARY KEY CHECK (length(token_hash) = 32),
    session_id uuid        NOT NULL REFERENCES iam.sessions (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    CHECK (expires_at > created_at)
);

CREATE INDEX refresh_tokens_session_id_idx ON iam.refresh_tokens (session_id);

-- +goose Down
DROP TABLE iam.refresh_tokens;
DROP TABLE iam.sessions;
