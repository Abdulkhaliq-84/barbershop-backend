-- booking: the appointment lifecycle (ADR-0025). An appointment keeps the
-- cancellation deadline it was booked under, as it keeps its prices: a
-- later change to the branch's policy doesn't move it.

-- +goose Up
ALTER TABLE booking.appointments ADD COLUMN cancellable_until timestamptz;
UPDATE booking.appointments SET cancellable_until = starts_at;
ALTER TABLE booking.appointments
    ALTER COLUMN cancellable_until SET NOT NULL,
    ADD CONSTRAINT appointments_cancellable_until_check CHECK (cancellable_until <= starts_at);

-- +goose Down
ALTER TABLE booking.appointments DROP COLUMN cancellable_until;
