-- notification's queries. They touch only the notification schema.

-- name: SaveDevice :one
-- Registers a device by its token. The same token registered again keeps
-- its row and refreshes it; registered by another user (another account
-- signed in on the phone), it becomes a new registration of theirs.
INSERT INTO notification.devices (id, user_id, token, platform, locale, created_at, updated_at)
VALUES (@id, @user_id, @token, @platform, @locale, @now, @now)
ON CONFLICT (token) DO UPDATE SET
    id         = CASE WHEN notification.devices.user_id = excluded.user_id
                      THEN notification.devices.id ELSE excluded.id END,
    created_at = CASE WHEN notification.devices.user_id = excluded.user_id
                      THEN notification.devices.created_at ELSE excluded.created_at END,
    user_id    = excluded.user_id,
    platform   = excluded.platform,
    locale     = excluded.locale,
    updated_at = excluded.updated_at
RETURNING id, user_id, platform, locale, created_at, updated_at;

-- name: TrimDevices :exec
-- Keeps only the user's newest devices.
DELETE FROM notification.devices AS old
WHERE old.user_id = @user_id
  AND old.id NOT IN (SELECT d.id FROM notification.devices d
                     WHERE d.user_id = @user_id
                     ORDER BY d.updated_at DESC, d.id DESC
                     LIMIT @keep);

-- name: RemoveDevice :execrows
DELETE FROM notification.devices WHERE id = @id AND user_id = @user_id;

-- name: DevicesOfUser :many
-- A user's devices, newest first.
SELECT id, user_id, token, platform, locale, created_at, updated_at
FROM notification.devices
WHERE user_id = @user_id
ORDER BY updated_at DESC, id DESC;

-- name: KeepBranch :execrows
-- Saves notification's copy of a branch, unless the copy already holds this
-- version or a newer one: events can arrive twice and out of order.
INSERT INTO notification.branches (branch_id, version, name_ar, name_en, timezone)
VALUES (@branch_id, @version, @name_ar, @name_en, @timezone)
ON CONFLICT (branch_id) DO UPDATE SET
    version  = excluded.version,
    name_ar  = excluded.name_ar,
    name_en  = excluded.name_en,
    timezone = excluded.timezone
WHERE notification.branches.version < excluded.version;

-- name: BranchByID :one
SELECT branch_id, version, name_ar, name_en, timezone
FROM notification.branches
WHERE branch_id = @branch_id;

-- name: Delivered :one
SELECT EXISTS (SELECT FROM notification.deliveries WHERE event_id = @event_id AND device_id = @device_id);

-- name: RecordDelivery :exec
-- Logs a push sent. Recorded twice (a retry), it stays one row.
INSERT INTO notification.deliveries (event_id, device_id, user_id, kind, appointment_id, sent_at)
VALUES (@event_id, @device_id, @user_id, @kind, @appointment_id, @sent_at)
ON CONFLICT (event_id, device_id) DO NOTHING;
