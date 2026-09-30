-- The outbox's job tables and billing's subscriptions (ADR-0009, ADR-0019).
--
-- The river schema starts empty: River creates and upgrades its own tables
-- with its own versioned migrator, which database.Migrate runs right after
-- these files. Keeping River's SQL out of this folder means upgrading River
-- never needs a hand-copied migration.

-- +goose Up
CREATE SCHEMA river;

CREATE SCHEMA billing;

-- One subscription per business, created when the business is approved.
-- business_id has no foreign key into the business schema (ADR-0015).
-- Only trials exist so far; paid statuses arrive with payments.
CREATE TABLE billing.subscriptions (
    business_id        uuid PRIMARY KEY,
    plan_code          text        NOT NULL CHECK (plan_code IN ('free', 'pro')),
    status             text        NOT NULL CHECK (status IN ('trialing')),
    current_period_end timestamptz NOT NULL,
    created_at         timestamptz NOT NULL,
    CHECK (current_period_end > created_at)
);

-- +goose Down
DROP TABLE billing.subscriptions;
DROP SCHEMA billing;
-- CASCADE: River's own tables live here.
DROP SCHEMA river CASCADE;
