-- booking: staff bookings for walk-in customers, and the pending-expiry job
-- (ADR-0026). A walk-in has no app account: the shop records a name.

-- +goose Up
ALTER TABLE booking.appointments
    ALTER COLUMN customer_id DROP NOT NULL,
    ADD COLUMN customer_name text NOT NULL DEFAULT '' CHECK (char_length(customer_name) <= 100),
    ADD CONSTRAINT appointments_customer_check
        CHECK (customer_id IS NOT NULL OR (source = 'staff' AND customer_name <> ''));

-- An Idempotency-Key belongs to whoever asked: the customer, or the staff
-- member who booked a walk-in.
ALTER TABLE booking.idempotency_keys RENAME COLUMN customer_id TO requester_id;

-- The expiry job's question, every minute: which pending bookings are due?
CREATE INDEX appointments_pending_idx ON booking.appointments (pending_until) WHERE status = 'pending';

-- +goose Down
DROP INDEX booking.appointments_pending_idx;
DELETE FROM booking.idempotency_keys WHERE appointment_id IN (SELECT id FROM booking.appointments WHERE customer_id IS NULL);
DELETE FROM booking.appointment_items WHERE appointment_id IN (SELECT id FROM booking.appointments WHERE customer_id IS NULL);
DELETE FROM booking.appointments WHERE customer_id IS NULL;
ALTER TABLE booking.idempotency_keys RENAME COLUMN requester_id TO customer_id;
ALTER TABLE booking.appointments
    DROP CONSTRAINT appointments_customer_check,
    DROP COLUMN customer_name,
    ALTER COLUMN customer_id SET NOT NULL;
