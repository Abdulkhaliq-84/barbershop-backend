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
