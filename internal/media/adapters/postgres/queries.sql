-- media queries. Only the media schema, never another module's.
-- Run `make generate` after editing; sqlc writes sqlcgen/.

-- name: InsertObject :exec
INSERT INTO media.objects (id, purpose, content_type, size_bytes, sha256, created_by, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ObjectByID :one
SELECT * FROM media.objects WHERE id = $1;

-- name: DeleteObject :exec
DELETE FROM media.objects WHERE id = $1;
