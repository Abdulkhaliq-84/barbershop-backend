-- business queries. Only the business schema, never another module's.
-- Run `make generate` after editing; sqlc writes sqlcgen/.

-- name: InsertBusiness :exec
INSERT INTO business.businesses (id, owner_user_id, display_name_ar, display_name_en, legal_name, cr_number, status, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: InsertStaffMember :exec
INSERT INTO business.staff_members (id, business_id, user_id, role, active, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: BusinessByID :one
SELECT * FROM business.businesses WHERE id = $1;

-- name: StaffMembership :one
SELECT * FROM business.staff_members WHERE business_id = $1 AND user_id = $2;

-- name: MembershipsForUser :many
SELECT s.id AS staff_id, s.role, b.id AS business_id, b.display_name_ar, b.display_name_en, b.status
FROM business.staff_members s
JOIN business.businesses b ON b.id = s.business_id
WHERE s.user_id = $1 AND s.active
ORDER BY b.id DESC; -- UUIDv7: newest first

-- name: BusinessByIDForUpdate :one
-- Locks the row until the transaction ends: a second editor waits here.
SELECT * FROM business.businesses WHERE id = $1 FOR UPDATE;

-- name: UpdateBusiness :execrows
-- The version check is repeated in the WHERE clause as a second guard:
-- it matches no row if the version moved since the caller read it.
UPDATE business.businesses
SET display_name_ar = @display_name_ar, display_name_en = @display_name_en, legal_name = @legal_name,
    status = @status, version = @version, updated_at = @updated_at
WHERE id = @id AND version = @expected_version;
