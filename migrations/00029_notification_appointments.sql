-- notification's copy of each customer's booking, kept from booking's events,
-- for reminders (ADR-0035). Walk-ins have no customer to remind and aren't
-- kept. A status only moves forward: pending → confirmed → closed.

-- +goose Up
CREATE TABLE notification.appointments (
    appointment_id uuid PRIMARY KEY,
    customer_id    uuid        NOT NULL,
    branch_id      uuid        NOT NULL,
    starts_at      timestamptz NOT NULL,
    status         text        NOT NULL CHECK (status IN ('pending', 'confirmed', 'closed')),
    confirmed_at   timestamptz,          -- when it was confirmed
    reminded_at    timestamptz,          -- when its reminder was queued
    CHECK (status <> 'confirmed' OR confirmed_at IS NOT NULL),
    CHECK (status <> 'pending' OR confirmed_at IS NULL)
);
-- The reminder task's search: confirmed, not reminded, by start.
CREATE INDEX appointments_due_idx ON notification.appointments (starts_at)
    WHERE status = 'confirmed' AND reminded_at IS NULL;

-- +goose Down
DROP TABLE notification.appointments;
