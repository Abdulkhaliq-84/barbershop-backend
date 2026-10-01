-- scheduling's queries. Only the scheduling schema is touched here, and
-- every query is scoped by business and branch.

-- name: CalendarByBranch :one
SELECT * FROM scheduling.branch_calendars WHERE business_id = $1 AND branch_id = $2;

-- name: CalendarForUpdate :one
SELECT * FROM scheduling.branch_calendars WHERE business_id = $1 AND branch_id = $2 FOR UPDATE;

-- name: InsertCalendar :exec
INSERT INTO scheduling.branch_calendars (business_id, branch_id, version, updated_at) VALUES ($1, $2, $3, $4);

-- name: UpdateCalendar :execrows
UPDATE scheduling.branch_calendars SET version = @version, updated_at = @updated_at
WHERE business_id = @business_id AND branch_id = @branch_id AND version = @expected_version;

-- name: OpeningHours :many
SELECT * FROM scheduling.opening_hours WHERE business_id = $1 AND branch_id = $2 ORDER BY weekday, start_minute;

-- name: DeleteOpeningHours :exec
DELETE FROM scheduling.opening_hours WHERE business_id = $1 AND branch_id = $2;

-- name: InsertOpeningHour :exec
INSERT INTO scheduling.opening_hours (business_id, branch_id, weekday, start_minute, duration_minutes) VALUES ($1, $2, $3, $4, $5);

-- name: ScheduleByKey :one
SELECT * FROM scheduling.barber_schedules WHERE business_id = $1 AND branch_id = $2 AND staff_id = $3;

-- name: ScheduleForUpdate :one
SELECT * FROM scheduling.barber_schedules WHERE business_id = $1 AND branch_id = $2 AND staff_id = $3 FOR UPDATE;

-- name: InsertSchedule :exec
INSERT INTO scheduling.barber_schedules (business_id, branch_id, staff_id, version, updated_at) VALUES ($1, $2, $3, $4, $5);

-- name: UpdateSchedule :execrows
UPDATE scheduling.barber_schedules SET version = @version, updated_at = @updated_at
WHERE business_id = @business_id AND branch_id = @branch_id AND staff_id = @staff_id AND version = @expected_version;

-- name: BarberHours :many
SELECT * FROM scheduling.barber_hours
WHERE business_id = $1 AND branch_id = $2 AND staff_id = $3
ORDER BY weekday, start_minute;

-- name: BarberHoursElsewhere :many
-- The same person's weekly hours at their other branches.
SELECT * FROM scheduling.barber_hours
WHERE business_id = @business_id AND staff_id = @staff_id AND branch_id <> @branch_id
ORDER BY branch_id, weekday, start_minute;

-- name: DeleteBarberHours :exec
DELETE FROM scheduling.barber_hours WHERE business_id = $1 AND branch_id = $2 AND staff_id = $3;

-- name: InsertBarberHour :exec
INSERT INTO scheduling.barber_hours (business_id, branch_id, staff_id, weekday, start_minute, duration_minutes) VALUES ($1, $2, $3, $4, $5, $6);

-- name: OverrideHours :many
-- Every override date, with its hours; a date with no hours is a day off.
SELECT o.on_date, h.start_minute, h.duration_minutes
FROM scheduling.schedule_overrides o
LEFT JOIN scheduling.override_hours h
  ON h.business_id = o.business_id AND h.branch_id = o.branch_id AND h.staff_id = o.staff_id AND h.on_date = o.on_date
WHERE o.business_id = $1 AND o.branch_id = $2 AND o.staff_id = $3
ORDER BY o.on_date, h.start_minute;

-- name: DeleteOverrideHours :exec
DELETE FROM scheduling.override_hours WHERE business_id = $1 AND branch_id = $2 AND staff_id = $3;

-- name: DeleteOverrides :exec
DELETE FROM scheduling.schedule_overrides WHERE business_id = $1 AND branch_id = $2 AND staff_id = $3;

-- name: InsertOverride :exec
INSERT INTO scheduling.schedule_overrides (business_id, branch_id, staff_id, on_date) VALUES ($1, $2, $3, $4);

-- name: InsertOverrideHour :exec
INSERT INTO scheduling.override_hours (business_id, branch_id, staff_id, on_date, start_minute, duration_minutes) VALUES ($1, $2, $3, $4, $5, $6);

-- name: InsertTimeOff :exec
INSERT INTO scheduling.time_off (id, business_id, staff_id, starts_at, ends_at, reason, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: TimeOffByStaff :many
SELECT * FROM scheduling.time_off
WHERE business_id = $1 AND staff_id = $2 AND ends_at > $3
ORDER BY starts_at;

-- name: DeleteTimeOff :execrows
DELETE FROM scheduling.time_off WHERE business_id = $1 AND staff_id = $2 AND id = $3;
