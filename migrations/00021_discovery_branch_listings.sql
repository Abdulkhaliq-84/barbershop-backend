-- discovery: the branch listings customers browse (ADR-0027). A read model:
-- discovery's own copy of each branch, kept from business's branch events
-- and never written by an API request. branch_id and business_id have no
-- foreign keys into the business schema (ADR-0015).

-- +goose Up
CREATE SCHEMA discovery;

CREATE TABLE discovery.branch_listings (
    branch_id   uuid PRIMARY KEY,
    business_id uuid             NOT NULL,
    version     integer          NOT NULL CHECK (version > 0), -- the branch's version this copy is of
    listed      boolean          NOT NULL,                     -- published: customers can find it
    name_ar     text COLLATE "C" NOT NULL CHECK (name_ar <> ''), -- see the index below
    name_en     text             NOT NULL DEFAULT '',
    city_code   text             NOT NULL,
    district    text             NOT NULL DEFAULT '',
    address     text             NOT NULL,
    latitude    double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude   double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    phone       text             NOT NULL DEFAULT '',          -- E.164; '' if none
    timezone    text             NOT NULL,
    updated_at  timestamptz      NOT NULL                      -- when the branch changed
);

-- Browsing a city: its listed branches by Arabic name. name_ar sorts in "C"
-- (code point) order, the same on every machine, so a cursor means the same
-- thing in development, CI and production.
CREATE INDEX branch_listings_city_idx
    ON discovery.branch_listings (city_code, name_ar, branch_id)
    WHERE listed;

-- +goose Down
DROP TABLE discovery.branch_listings;
DROP SCHEMA discovery;
