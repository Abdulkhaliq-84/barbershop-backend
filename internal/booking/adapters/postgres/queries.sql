-- booking's queries. Only the booking schema is touched here.

-- name: BusyIntervals :many
-- Active appointments of these barbers overlapping [from, to): the time
-- each can't take another booking (the exclusion constraint's own index
-- serves it).
SELECT staff_id, lower(during)::timestamptz AS busy_from, upper(during)::timestamptz AS busy_to
FROM booking.appointments
WHERE staff_id = ANY(@staff::uuid[])
  AND status IN ('pending', 'confirmed')
  AND during && tstzrange(@from_time::timestamptz, @to_time::timestamptz)
ORDER BY staff_id, lower(during);

-- name: ClaimIdempotencyKey :execrows
-- 1 row: this request is the first with the key. 0 rows: another request
-- has it; if that one is still running, this waits for it to finish.
INSERT INTO booking.idempotency_keys (customer_id, key, request_hash, created_at)
VALUES (@customer_id, @key, @request_hash, @created_at)
ON CONFLICT DO NOTHING;

-- name: IdempotencyKey :one
SELECT request_hash, appointment_id FROM booking.idempotency_keys WHERE customer_id = $1 AND key = $2;

-- name: SettleIdempotencyKey :exec
UPDATE booking.idempotency_keys SET appointment_id = @appointment_id WHERE customer_id = @customer_id AND key = @key;

-- name: CountActiveBookings :one
-- The customer's upcoming active bookings at the branch.
SELECT count(*) FROM booking.appointments
WHERE customer_id = @customer_id AND branch_id = @branch_id
  AND status IN ('pending', 'confirmed') AND starts_at > @now;

-- name: LockCustomerAtBranch :exec
-- One booking at a time per customer and branch, so two at once can't both
-- pass the limit on upcoming bookings.
SELECT pg_advisory_xact_lock(hashtextextended('booking.customer:' || @customer_id::text || ':' || @branch_id::text, 0));

-- name: InsertAppointment :exec
INSERT INTO booking.appointments (
    id, business_id, branch_id, staff_id, customer_id, status, source, assignment,
    starts_at, ends_at, during, price_amount, price_currency, customer_note, pending_until,
    version, created_at, updated_at
) VALUES (
    @id, @business_id, @branch_id, @staff_id, @customer_id, @status, @source, @assignment,
    @starts_at, @ends_at, tstzrange(@starts_at, @busy_until::timestamptz, '[)'), @price_amount, @price_currency,
    @customer_note, @pending_until, @version, @created_at, @updated_at
);

-- name: InsertAppointmentItem :exec
INSERT INTO booking.appointment_items (appointment_id, position, service_id, name_ar, name_en, duration_minutes, price_amount, price_currency)
VALUES (@appointment_id, @position, @service_id, @name_ar, @name_en, @duration_minutes, @price_amount, @price_currency);

-- name: CustomerAppointment :one
-- Always by (customer_id, id): someone else's appointment ID finds nothing.
SELECT id, business_id, branch_id, staff_id, customer_id, status, source, assignment, starts_at, ends_at,
       upper(during)::timestamptz AS busy_until, price_amount, price_currency, customer_note, pending_until,
       version, created_at, updated_at
FROM booking.appointments WHERE customer_id = $1 AND id = $2;

-- name: AppointmentItems :many
SELECT * FROM booking.appointment_items WHERE appointment_id = $1 ORDER BY position;
