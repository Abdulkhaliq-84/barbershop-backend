-- business: branches — the shops customers visit (docs/architecture/domain-model.md §3.2).
-- A branch belongs to one business (tenant) and is always read and written
-- together with its business_id, so one shop's IDs never open another's rows.
--
-- The location is kept as plain latitude/longitude: this module only stores
-- it. Map search ("near me") is discovery's job, on its own PostGIS read
-- model (M6), fed by branch events.

-- +goose Up
CREATE TABLE business.branches (
    id                  uuid PRIMARY KEY,            -- UUIDv7, generated in Go
    business_id         uuid        NOT NULL REFERENCES business.businesses (id),
    name_ar             text        NOT NULL CHECK (name_ar <> ''),
    name_en             text        NOT NULL DEFAULT '',
    city_code           text        NOT NULL CHECK (city_code ~ '^[a-z][a-z_]{1,39}$'),
    district            text        NOT NULL DEFAULT '',
    address             text        NOT NULL CHECK (address <> ''),
    latitude            double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude           double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    phone               text,                          -- E.164, optional
    timezone            text        NOT NULL,          -- IANA name, e.g. Asia/Riyadh
    status              text        NOT NULL CHECK (status IN ('draft', 'published', 'unpublished')),

    -- Booking policy (value object on the branch): the rules customers book by.
    min_lead_minutes          integer  NOT NULL CHECK (min_lead_minutes BETWEEN 0 AND 10080),
    horizon_days              smallint NOT NULL CHECK (horizon_days BETWEEN 1 AND 180),
    slot_interval_minutes     smallint NOT NULL CHECK (slot_interval_minutes IN (5, 10, 15, 20, 30, 60)),
    buffer_minutes            smallint NOT NULL CHECK (buffer_minutes BETWEEN 0 AND 60),
    cancellation_minutes      integer  NOT NULL CHECK (cancellation_minutes BETWEEN 0 AND 2880),
    auto_confirm              boolean  NOT NULL,
    pending_expiry_minutes    smallint NOT NULL CHECK (pending_expiry_minutes BETWEEN 5 AND 120),
    max_active_bookings       smallint NOT NULL CHECK (max_active_bookings BETWEEN 1 AND 10),

    version             integer     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL
);

-- Every branch query starts from the business: list its branches, or load one
-- by (business_id, id).
CREATE INDEX branches_business_id_idx ON business.branches (business_id, id);

-- +goose Down
DROP TABLE business.branches;
