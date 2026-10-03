-- What the push service did with each push (ADR-0034): sent, or rejected for
-- good (an invalid token or message), which is never tried again. Devices
-- the service no longer knows are deleted instead.

-- +goose Up
ALTER TABLE notification.deliveries
    ADD COLUMN outcome text NOT NULL DEFAULT 'sent' CHECK (outcome IN ('sent', 'rejected'));
-- Rows before this migration were all sent; from now on the code says.
ALTER TABLE notification.deliveries ALTER COLUMN outcome DROP DEFAULT;

-- +goose Down
ALTER TABLE notification.deliveries DROP COLUMN outcome;
