-- discovery: what each branch sells, for the category filter and the price
-- from (ADR-0030). discovery's own copy of each service, kept from catalog's
-- service events and never written by an API request. No foreign keys: not
-- into catalog (ADR-0015), and not to branch_listings either, as a branch's
-- services exist (and their events arrive) before it is published.

-- +goose Up
CREATE TABLE discovery.branch_services (
    service_id    uuid PRIMARY KEY,
    branch_id     uuid        NOT NULL,
    business_id   uuid        NOT NULL,
    version       integer     NOT NULL CHECK (version > 0), -- the service's version this copy is of
    offered       boolean     NOT NULL,                     -- active, and someone performs it
    category_code text        NOT NULL,
    price_from    bigint      NOT NULL CHECK (price_from >= 0), -- halalas: catalog's prices are SAR
    updated_at    timestamptz NOT NULL                      -- when the service changed
);

-- A branch's offered services by category, then price: "does it offer a
-- beard trim?" and "from how much?" are answered from the index alone.
CREATE INDEX branch_services_branch_idx
    ON discovery.branch_services (branch_id, category_code, price_from)
    WHERE offered;

-- +goose Down
DROP TABLE discovery.branch_services;
