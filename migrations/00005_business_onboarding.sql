-- business: tenants and their staff (docs/architecture/domain-model.md §3.2).
-- Each module owns one Postgres schema; no other module touches these tables.
--
-- User IDs come from iam but carry no foreign key into the iam schema: modules
-- only meet through their public Go APIs, never through each other's tables
-- (ADR-0015). The IDs come from verified access tokens, so they exist.

-- +goose Up
CREATE SCHEMA business;

CREATE TABLE business.businesses (
    id              uuid PRIMARY KEY,               -- UUIDv7, generated in Go
    owner_user_id   uuid        NOT NULL,
    display_name_ar text        NOT NULL CHECK (display_name_ar <> ''),
    display_name_en text        NOT NULL DEFAULT '',
    legal_name      text        NOT NULL CHECK (legal_name <> ''),
    cr_number       text        NOT NULL CHECK (cr_number ~ '^[0-9]{10}$'),
    status          text        NOT NULL CHECK (status IN ('draft', 'pending_review', 'active', 'rejected', 'suspended')),
    version         integer     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);

-- An owner registers a CR number once: a retried registration is refused
-- instead of creating a second draft.
CREATE UNIQUE INDEX businesses_owner_cr_number_key ON business.businesses (owner_user_id, cr_number);

-- A CR number is claimed platform-wide only once the business is submitted.
-- Drafts don't claim it, so nobody can block a real shop by registering its
-- number first; the reviewer checks the CR document before approving.
CREATE UNIQUE INDEX businesses_claimed_cr_number_key ON business.businesses (cr_number)
    WHERE status IN ('pending_review', 'active', 'suspended');

-- Staff: who works in which business, and as what. user_id stays empty for
-- invited staff until they accept (invitations arrive later in M3).
CREATE TABLE business.staff_members (
    id          uuid PRIMARY KEY,
    business_id uuid        NOT NULL REFERENCES business.businesses (id),
    user_id     uuid,
    role        text        NOT NULL CHECK (role IN ('owner', 'manager', 'barber')),
    active      boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL
);

-- Exactly one owner per business: the domain creates one with every business,
-- and the database refuses a second.
CREATE UNIQUE INDEX staff_members_one_owner_key ON business.staff_members (business_id) WHERE role = 'owner';

-- A user is staff of a business at most once. It also serves the membership
-- check that opens every business-mode request, and "my memberships".
CREATE UNIQUE INDEX staff_members_user_business_key ON business.staff_members (user_id, business_id)
    WHERE user_id IS NOT NULL;

-- +goose Down
DROP TABLE business.staff_members;
DROP TABLE business.businesses;
DROP SCHEMA business;
