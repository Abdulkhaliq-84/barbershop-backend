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
