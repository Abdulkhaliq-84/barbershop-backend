-- booking: Idempotency-Key for booking requests (ADR-0024). A mobile app
-- retrying a booking after a timeout gets the appointment it already made,
-- not a second one.

-- +goose Up
CREATE TABLE booking.idempotency_keys (
    customer_id    uuid        NOT NULL,
    key            uuid        NOT NULL,
    request_hash   bytea       NOT NULL CHECK (length(request_hash) = 32), -- SHA-256 of what was asked
    appointment_id uuid        REFERENCES booking.appointments (id),       -- set before the transaction commits
    created_at     timestamptz NOT NULL,
    PRIMARY KEY (customer_id, key)
);

-- +goose Down
DROP TABLE booking.idempotency_keys;
