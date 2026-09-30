-- business queries. Only the business schema, never another module's.
-- Run `make generate` after editing; sqlc writes sqlcgen/.

-- name: InsertBusiness :exec
INSERT INTO business.businesses (id, owner_user_id, display_name_ar, display_name_en, legal_name, cr_number, status, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: InsertStaffMember :exec
INSERT INTO business.staff_members (id, business_id, user_id, role, active, created_at, display_name)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: InsertStaffBranch :exec
INSERT INTO business.staff_branches (staff_id, branch_id, business_id) VALUES ($1, $2, $3);

-- name: BusinessByID :one
SELECT * FROM business.businesses WHERE id = $1;

-- name: StaffMembership :one
-- The caller's staff record with the branches they work at: the first query
-- of every business-mode request.
SELECT s.id, s.business_id, s.user_id, s.role, s.active, s.created_at, s.display_name,
       coalesce(array_agg(sb.branch_id) FILTER (WHERE sb.branch_id IS NOT NULL), '{}')::uuid[] AS branch_ids
FROM business.staff_members s
LEFT JOIN business.staff_branches sb ON sb.staff_id = s.id
WHERE s.business_id = $1 AND s.user_id = $2
GROUP BY s.id;

-- name: StaffByBusiness :many
SELECT s.id, s.business_id, s.user_id, s.role, s.active, s.created_at, s.display_name,
       coalesce(array_agg(sb.branch_id) FILTER (WHERE sb.branch_id IS NOT NULL), '{}')::uuid[] AS branch_ids
FROM business.staff_members s
LEFT JOIN business.staff_branches sb ON sb.staff_id = s.id
WHERE s.business_id = $1
GROUP BY s.id
ORDER BY s.created_at, s.id;

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
    status = @status, version = @version, updated_at = @updated_at,
    submitted_at = @submitted_at, reviewed_at = @reviewed_at, reviewed_by = @reviewed_by,
    rejection_reason = @rejection_reason
WHERE id = @id AND version = @expected_version;

-- name: InsertBranch :exec
INSERT INTO business.branches (
    id, business_id, name_ar, name_en, city_code, district, address, latitude, longitude, phone, timezone, status,
    min_lead_minutes, horizon_days, slot_interval_minutes, buffer_minutes, cancellation_minutes, auto_confirm,
    pending_expiry_minutes, max_active_bookings, version, created_at, updated_at
) VALUES (
    @id, @business_id, @name_ar, @name_en, @city_code, @district, @address, @latitude, @longitude, @phone, @timezone, @status,
    @min_lead_minutes, @horizon_days, @slot_interval_minutes, @buffer_minutes, @cancellation_minutes, @auto_confirm,
    @pending_expiry_minutes, @max_active_bookings, @version, @created_at, @updated_at
);

-- name: BranchByID :one
-- Always by (business_id, id): another business's branch ID finds nothing.
SELECT * FROM business.branches WHERE business_id = $1 AND id = $2;

-- name: BranchByIDForUpdate :one
SELECT * FROM business.branches WHERE business_id = $1 AND id = $2 FOR UPDATE;

-- name: BranchesByBusiness :many
SELECT * FROM business.branches WHERE business_id = $1 ORDER BY id; -- UUIDv7: oldest first

-- name: UpdateBranch :execrows
UPDATE business.branches
SET name_ar = @name_ar, name_en = @name_en, city_code = @city_code, district = @district, address = @address,
    latitude = @latitude, longitude = @longitude, phone = @phone, timezone = @timezone, status = @status,
    min_lead_minutes = @min_lead_minutes, horizon_days = @horizon_days, slot_interval_minutes = @slot_interval_minutes,
    buffer_minutes = @buffer_minutes, cancellation_minutes = @cancellation_minutes, auto_confirm = @auto_confirm,
    pending_expiry_minutes = @pending_expiry_minutes, max_active_bookings = @max_active_bookings,
    version = @version, updated_at = @updated_at
WHERE business_id = @business_id AND id = @id AND version = @expected_version;

-- name: CountVerificationDocuments :one
SELECT count(*) FROM business.verification_documents WHERE business_id = $1;

-- name: InsertVerificationDocument :exec
INSERT INTO business.verification_documents (object_id, business_id, kind, content_type, size_bytes, uploaded_by, uploaded_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: VerificationDocumentsByBusiness :many
SELECT * FROM business.verification_documents WHERE business_id = $1 ORDER BY uploaded_at, object_id;

-- name: CountBranches :one
SELECT count(*) FROM business.branches WHERE business_id = $1;

-- name: BusinessesForReview :many
-- One page of the admin queue: one status, oldest submission first. The
-- cursor is the (submitted_at, id) of the last row of the previous page.
SELECT * FROM business.businesses
WHERE status = @status
  AND (sqlc.narg('after_submitted_at')::timestamptz IS NULL
       OR (submitted_at, id) > (sqlc.narg('after_submitted_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY submitted_at, id
LIMIT @page_size;

-- name: CountBranchesIn :one
SELECT count(*) FROM business.branches WHERE business_id = @business_id AND id = ANY(@ids::uuid[]);

-- name: LockBusiness :one
-- Takes turns between additions to one business (invitations, branches), so
-- two at once can't both pass a count check. NO KEY UPDATE doesn't block
-- rows that merely reference the business (their foreign-key checks).
SELECT id FROM business.businesses WHERE id = $1 FOR NO KEY UPDATE;

-- name: CountStaffSeats :one
-- Seats in use: active managers and barbers, plus invitations that can
-- still be accepted. The owner doesn't take a seat.
SELECT ((SELECT count(*) FROM business.staff_members s
          WHERE s.business_id = @business_id AND s.role <> 'owner' AND s.active)
      + (SELECT count(*) FROM business.invitations i
          WHERE i.business_id = @business_id AND i.status = 'pending' AND i.expires_at > @now::timestamptz))::int AS seats;

-- name: RevokePendingInvitations :exec
UPDATE business.invitations SET status = 'revoked'
WHERE business_id = $1 AND phone = $2 AND status = 'pending';

-- name: InsertInvitation :exec
INSERT INTO business.invitations (id, business_id, phone, display_name, role, branch_ids, token_hash, status, invited_by, created_at, expires_at)
VALUES (@id, @business_id, @phone, @display_name, @role, @branch_ids, @token_hash, @status, @invited_by, @created_at, @expires_at);

-- name: PendingInvitations :many
SELECT * FROM business.invitations WHERE business_id = $1 AND status = 'pending' ORDER BY created_at DESC, id;

-- name: InvitationForUpdate :one
SELECT * FROM business.invitations WHERE business_id = $1 AND id = $2 FOR UPDATE;

-- name: InvitationByTokenForUpdate :one
SELECT * FROM business.invitations WHERE token_hash = $1 FOR UPDATE;

-- name: SaveInvitation :exec
UPDATE business.invitations SET status = @status, accepted_at = @accepted_at, accepted_by = @accepted_by
WHERE id = @id;
