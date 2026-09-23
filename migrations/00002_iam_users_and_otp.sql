-- iam: identity & access (docs/architecture/domain-model.md §3.1).
-- Each module owns one Postgres schema; no other module touches these tables.

-- +goose Up
CREATE SCHEMA iam;

CREATE TABLE iam.users (
    id            uuid PRIMARY KEY,                -- UUIDv7, generated in Go
    phone         text        NOT NULL UNIQUE,     -- E.164, e.g. +966551234567
    name          text,                            -- set during onboarding
    locale        text        NOT NULL DEFAULT 'ar' CHECK (locale IN ('ar', 'en')),
    status        text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
    platform_role text        NOT NULL DEFAULT 'none' CHECK (platform_role IN ('none', 'admin')),
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL
);

-- One row per code sent. The code itself is never stored: only an HMAC of it,
-- so a leaked database can't be used to sign in (or brute-forced offline).
CREATE TABLE iam.otp_challenges (
    id          uuid PRIMARY KEY,
    phone       text        NOT NULL,
    code_hash   bytea       NOT NULL,
    attempts    smallint    NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    CHECK (expires_at > created_at)
);

-- Serves "latest code for this phone" and "codes for this phone in the last hour".
CREATE INDEX otp_challenges_phone_created_at_idx ON iam.otp_challenges (phone, created_at DESC);

-- +goose Down
DROP TABLE iam.otp_challenges;
DROP TABLE iam.users;
DROP SCHEMA iam;
