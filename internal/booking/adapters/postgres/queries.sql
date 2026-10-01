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
INSERT INTO booking.idempotency_keys (requester_id, key, request_hash, created_at)
VALUES (@requester_id, @key, @request_hash, @created_at)
ON CONFLICT DO NOTHING;

-- name: IdempotencyKey :one
SELECT request_hash, appointment_id FROM booking.idempotency_keys WHERE requester_id = $1 AND key = $2;

-- name: SettleIdempotencyKey :exec
UPDATE booking.idempotency_keys SET appointment_id = @appointment_id WHERE requester_id = @requester_id AND key = @key;

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
    id, business_id, branch_id, staff_id, customer_id, customer_name, status, source, assignment,
    starts_at, ends_at, during, price_amount, price_currency, customer_note, pending_until,
    cancellable_until, version, created_at, updated_at
) VALUES (
    @id, @business_id, @branch_id, @staff_id, @customer_id, @customer_name, @status, @source, @assignment,
    @starts_at, @ends_at, tstzrange(@starts_at, @busy_until::timestamptz, '[)'), @price_amount, @price_currency,
    @customer_note, @pending_until, @cancellable_until, @version, @created_at, @updated_at
);

-- name: InsertAppointmentItem :exec
INSERT INTO booking.appointment_items (appointment_id, position, service_id, name_ar, name_en, duration_minutes, price_amount, price_currency)
VALUES (@appointment_id, @position, @service_id, @name_ar, @name_en, @duration_minutes, @price_amount, @price_currency);

-- The appointment queries below select the same columns, in the same
-- order: the repository reads all their rows the same way.

-- name: CustomerAppointment :one
-- Always by (customer_id, id): someone else's appointment ID finds nothing.
SELECT id, business_id, branch_id, staff_id, customer_id, customer_name, status, source, assignment, starts_at, ends_at,
       upper(during)::timestamptz AS busy_until, price_amount, price_currency, customer_note, pending_until,
       cancellable_until, cancelled_by, cancel_reason, cancelled_at, version, created_at, updated_at
FROM booking.appointments WHERE customer_id = $1 AND id = $2;

-- name: CustomerAppointmentForUpdate :one
-- CustomerAppointment, locked until the transaction ends: one change at a time.
SELECT id, business_id, branch_id, staff_id, customer_id, customer_name, status, source, assignment, starts_at, ends_at,
       upper(during)::timestamptz AS busy_until, price_amount, price_currency, customer_note, pending_until,
       cancellable_until, cancelled_by, cancel_reason, cancelled_at, version, created_at, updated_at
FROM booking.appointments WHERE customer_id = $1 AND id = $2 FOR UPDATE;

-- name: BusinessAppointmentForUpdate :one
-- By (business_id, id), locked: another business's appointment ID finds nothing.
SELECT id, business_id, branch_id, staff_id, customer_id, customer_name, status, source, assignment, starts_at, ends_at,
       upper(during)::timestamptz AS busy_until, price_amount, price_currency, customer_note, pending_until,
       cancellable_until, cancelled_by, cancel_reason, cancelled_at, version, created_at, updated_at
FROM booking.appointments WHERE business_id = $1 AND id = $2 FOR UPDATE;

-- name: BranchDay :many
-- A branch's appointments starting in [from, to), every status, by start;
-- only one barber's when staff_id is given.
SELECT id, business_id, branch_id, staff_id, customer_id, customer_name, status, source, assignment, starts_at, ends_at,
       upper(during)::timestamptz AS busy_until, price_amount, price_currency, customer_note, pending_until,
       cancellable_until, cancelled_by, cancel_reason, cancelled_at, version, created_at, updated_at
FROM booking.appointments
WHERE business_id = @business_id AND branch_id = @branch_id
  AND starts_at >= @from_time AND starts_at < @to_time
  AND (sqlc.narg(staff_id)::uuid IS NULL OR staff_id = sqlc.narg(staff_id))
ORDER BY starts_at, id;

-- name: AppointmentByID :one
-- For a replay: the Idempotency-Key, found by its owner, already scoped it.
SELECT id, business_id, branch_id, staff_id, customer_id, customer_name, status, source, assignment, starts_at, ends_at,
       upper(during)::timestamptz AS busy_until, price_amount, price_currency, customer_note, pending_until,
       cancellable_until, cancelled_by, cancel_reason, cancelled_at, version, created_at, updated_at
FROM booking.appointments WHERE id = $1;

-- name: DuePendingForUpdate :many
-- Pending bookings whose expiry has passed, oldest first, locked. Rows
-- another transaction holds (the shop confirming one right now) are
-- skipped: the next run sees them again if they are still pending.
SELECT id, business_id, branch_id, staff_id, customer_id, customer_name, status, source, assignment, starts_at, ends_at,
       upper(during)::timestamptz AS busy_until, price_amount, price_currency, customer_note, pending_until,
       cancellable_until, cancelled_by, cancel_reason, cancelled_at, version, created_at, updated_at
FROM booking.appointments
WHERE status = 'pending' AND pending_until <= @now
ORDER BY pending_until, id
LIMIT @max_rows
FOR UPDATE SKIP LOCKED;

-- name: UpdateAppointmentStatus :execrows
UPDATE booking.appointments
SET status = @status, pending_until = @pending_until, cancelled_by = @cancelled_by, cancel_reason = @cancel_reason,
    cancelled_at = @cancelled_at, version = @version, updated_at = @updated_at
WHERE id = @id AND version = @expected_version;

-- name: AppointmentItems :many
SELECT * FROM booking.appointment_items WHERE appointment_id = $1 ORDER BY position;

-- name: AppointmentItemsOf :many
SELECT * FROM booking.appointment_items WHERE appointment_id = ANY(@ids::uuid[]) ORDER BY appointment_id, position;
