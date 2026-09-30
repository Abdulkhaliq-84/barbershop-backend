-- booking: appointments (docs/architecture/domain-model.md §3.5, ADR-0008).
-- Availability (M5.2) reads when each barber is busy; booking (M5.3)
-- writes them.

-- +goose Up
CREATE SCHEMA booking;

CREATE TABLE booking.appointments (
    id             uuid PRIMARY KEY,               -- UUIDv7, generated in Go
    business_id    uuid        NOT NULL,
    branch_id      uuid        NOT NULL,
    staff_id       uuid        NOT NULL,           -- the barber
    customer_id    uuid        NOT NULL,           -- an iam user
    status         text        NOT NULL CHECK (status IN ('pending', 'confirmed', 'rejected', 'expired', 'cancelled', 'completed', 'no_show')),
    source         text        NOT NULL CHECK (source IN ('customer_app', 'staff')),
    assignment     text        NOT NULL CHECK (assignment IN ('requested_barber', 'any_barber')),
    starts_at      timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,           -- the services end; the branch's buffer follows
    -- What the barber can't do anything else in: [starts_at, ends_at + buffer).
    during         tstzrange   NOT NULL,
    price_amount   bigint      NOT NULL CHECK (price_amount BETWEEN 0 AND 50000000), -- halalas, the items' total
    price_currency char(3)     NOT NULL CHECK (price_currency = 'SAR'),
    customer_note  text        NOT NULL DEFAULT '' CHECK (char_length(customer_note) <= 300),
    pending_until  timestamptz,                    -- a pending booking expires then (M5.5)
    cancelled_by   text        CHECK (cancelled_by IN ('customer', 'staff')),
    cancel_reason  text        NOT NULL DEFAULT '' CHECK (char_length(cancel_reason) <= 300),
    cancelled_at   timestamptz,
    version        integer     NOT NULL CHECK (version > 0),
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    CHECK (ends_at > starts_at),
    CHECK (lower(during) = starts_at AND upper(during) >= ends_at AND lower_inc(during) AND NOT upper_inc(during)),
    CHECK ((status = 'pending') = (pending_until IS NOT NULL)),
    CHECK ((status = 'cancelled') = (cancelled_at IS NOT NULL AND cancelled_by IS NOT NULL)),
    -- ADR-0008: a barber never has two active appointments at once, however
    -- many requests race. A violation is SQLSTATE 23P01.
    CONSTRAINT appointments_no_overlap EXCLUDE USING gist (staff_id WITH =, during WITH &&)
        WHERE (status IN ('pending', 'confirmed'))
);

-- What was booked, as it was then: if the shop changes a price tomorrow,
-- today's booking keeps its own.
CREATE TABLE booking.appointment_items (
    appointment_id   uuid     NOT NULL REFERENCES booking.appointments (id),
    position         smallint NOT NULL CHECK (position BETWEEN 1 AND 5),
    service_id       uuid     NOT NULL,
    name_ar          text     NOT NULL CHECK (name_ar <> ''),
    name_en          text     NOT NULL DEFAULT '',
    duration_minutes smallint NOT NULL CHECK (duration_minutes BETWEEN 5 AND 480),
    price_amount     bigint   NOT NULL CHECK (price_amount BETWEEN 0 AND 10000000),
    price_currency   char(3)  NOT NULL CHECK (price_currency = 'SAR'),
    PRIMARY KEY (appointment_id, position)
);

-- "My appointments" (a customer's, by time) and a branch's day (the shop).
CREATE INDEX appointments_customer_idx ON booking.appointments (customer_id, starts_at);
CREATE INDEX appointments_branch_day_idx ON booking.appointments (business_id, branch_id, starts_at);

-- +goose Down
DROP TABLE booking.appointment_items;
DROP TABLE booking.appointments;
DROP SCHEMA booking;
