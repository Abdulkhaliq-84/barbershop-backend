-- catalog: the services each branch sells (docs/architecture/domain-model.md §3.3).
--
-- business_id and branch_id point into the business schema but carry no
-- foreign keys: modules meet only through their Go APIs (ADR-0015). The
-- catalog use cases ask business whether the branch is the business's
-- before anything is written here.

-- +goose Up
CREATE SCHEMA catalog;

CREATE TABLE catalog.services (
    id               uuid PRIMARY KEY,               -- UUIDv7, generated in Go
    business_id      uuid        NOT NULL,
    branch_id        uuid        NOT NULL,
    category_code    text        NOT NULL CHECK (category_code IN ('haircut', 'beard', 'shave', 'kids', 'skincare', 'colour', 'packages')),
    name_ar          text        NOT NULL CHECK (name_ar <> '' AND char_length(name_ar) <= 80),
    name_en          text        NOT NULL DEFAULT '' CHECK (char_length(name_en) <= 80),
    description_ar   text        NOT NULL DEFAULT '' CHECK (char_length(description_ar) <= 500),
    description_en   text        NOT NULL DEFAULT '' CHECK (char_length(description_en) <= 500),
    duration_minutes smallint    NOT NULL CHECK (duration_minutes BETWEEN 5 AND 480 AND duration_minutes % 5 = 0),
    price_amount     bigint      NOT NULL CHECK (price_amount BETWEEN 0 AND 10000000), -- halalas, VAT-inclusive
    price_currency   char(3)     NOT NULL CHECK (price_currency = 'SAR'),
    active           boolean     NOT NULL DEFAULT true,
    sort_order       smallint    NOT NULL DEFAULT 0 CHECK (sort_order BETWEEN 0 AND 1000),
    version          integer     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL
);

-- Every query starts from the branch, inside its business: list a branch's
-- menu in display order, or load one service by (business, branch, id).
CREATE INDEX services_branch_idx ON catalog.services (business_id, branch_id, sort_order, created_at);

-- +goose Down
DROP TABLE catalog.services;
DROP SCHEMA catalog;
