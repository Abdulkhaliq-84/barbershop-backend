-- business: staff invitations, staff names and staff branches
-- (docs/architecture/domain-model.md §3.2).
--
-- An owner invites a person by phone. The invitation carries a random token,
-- sent by SMS; only its SHA-256 is stored. Whoever accepts must hold the
-- token AND be signed in with the invited phone number. The staff row is
-- created only then, so staff user_id is always set (00005 left room for
-- "invited but not yet accepted" rows; invitations keep that state instead).

-- +goose Up
ALTER TABLE business.staff_members
    ADD COLUMN display_name text NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 80);

-- (business_id, id) keys, so staff_branches can require that a staff member
-- and a branch belong to the same business. The branches key replaces the
-- plain index on the same columns.
ALTER TABLE business.staff_members ADD CONSTRAINT staff_members_business_id_id_key UNIQUE (business_id, id);
DROP INDEX business.branches_business_id_idx;
ALTER TABLE business.branches ADD CONSTRAINT branches_business_id_id_key UNIQUE (business_id, id);

-- Which branches a manager or barber works at. The owner works everywhere
-- and has no rows here. Both foreign keys include business_id: the database
-- itself refuses to put one shop's barber in another shop's branch.
CREATE TABLE business.staff_branches (
    staff_id    uuid NOT NULL,
    branch_id   uuid NOT NULL,
    business_id uuid NOT NULL,
    PRIMARY KEY (staff_id, branch_id),
    FOREIGN KEY (business_id, staff_id) REFERENCES business.staff_members (business_id, id),
    FOREIGN KEY (business_id, branch_id) REFERENCES business.branches (business_id, id)
);

CREATE INDEX staff_branches_branch_idx ON business.staff_branches (branch_id);

CREATE TABLE business.invitations (
    id           uuid PRIMARY KEY,
    business_id  uuid        NOT NULL REFERENCES business.businesses (id),
    phone        text        NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'), -- E.164
    display_name text        NOT NULL CHECK (display_name <> '' AND char_length(display_name) <= 80),
    role         text        NOT NULL CHECK (role IN ('manager', 'barber')),
    branch_ids   uuid[]      NOT NULL CHECK (cardinality(branch_ids) BETWEEN 1 AND 50),
    token_hash   bytea       NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    status       text        NOT NULL CHECK (status IN ('pending', 'accepted', 'revoked')),
    invited_by   uuid        NOT NULL,
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    accepted_at  timestamptz,
    accepted_by  uuid,
    CHECK (expires_at > created_at),
    CHECK ((status = 'accepted') = (accepted_at IS NOT NULL AND accepted_by IS NOT NULL))
);

-- One open invitation per phone per business: inviting again replaces it.
CREATE UNIQUE INDEX invitations_one_pending_key ON business.invitations (business_id, phone)
    WHERE status = 'pending';

-- +goose Down
DROP TABLE business.invitations;
DROP TABLE business.staff_branches;
ALTER TABLE business.branches DROP CONSTRAINT branches_business_id_id_key;
CREATE INDEX branches_business_id_idx ON business.branches (business_id, id);
ALTER TABLE business.staff_members DROP CONSTRAINT staff_members_business_id_id_key;
ALTER TABLE business.staff_members DROP COLUMN display_name;
