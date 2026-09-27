-- Queries for the iam module. sqlc generates one Go method per query;
-- the comment line names it and says what it returns (:one, :many, :exec).

-- name: InsertOTPChallenge :exec
INSERT INTO iam.otp_challenges (id, phone, code_hash, attempts, created_at, expires_at, consumed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: LatestOTPChallenge :one
SELECT id, phone, code_hash, attempts, created_at, expires_at, consumed_at
FROM iam.otp_challenges
WHERE phone = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: LatestOTPChallengeForUpdate :one
-- Locks the row until the transaction ends, so two parallel guesses can't
-- both slip past the attempt limit.
SELECT id, phone, code_hash, attempts, created_at, expires_at, consumed_at
FROM iam.otp_challenges
WHERE phone = $1
ORDER BY created_at DESC
LIMIT 1
FOR UPDATE;

-- name: UpdateOTPChallenge :exec
UPDATE iam.otp_challenges
SET attempts = $2, consumed_at = $3
WHERE id = $1;

-- name: CountOTPChallengesSince :one
SELECT count(*)::int FROM iam.otp_challenges
WHERE phone = $1 AND created_at >= $2;

-- name: InsertUserIfNew :one
-- Registration is idempotent: a second verify for the same new number
-- (retry, double tap) returns the existing user instead of failing.
INSERT INTO iam.users (id, phone, locale, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (phone) DO NOTHING
RETURNING id;

-- name: UserByPhone :one
SELECT id, phone, name, locale, status, platform_role, created_at, updated_at
FROM iam.users
WHERE phone = $1;

-- name: LockOTPPhone :exec
-- Transaction lock also covers phones with no rows yet. Hash collisions only
-- serialize unrelated phones; they cannot bypass a limit. Seed namespaces IAM.
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 482193));

-- name: LoadOTPGuard :one
SELECT failures, window_start, locked_until FROM iam.otp_phone_guards WHERE phone = $1;

-- name: SaveOTPGuard :exec
INSERT INTO iam.otp_phone_guards (phone, failures, window_start, locked_until)
VALUES ($1, $2, $3, $4)
ON CONFLICT (phone) DO UPDATE SET failures = EXCLUDED.failures,
 window_start = EXCLUDED.window_start, locked_until = EXCLUDED.locked_until;

-- name: UserByID :one
SELECT id, phone, name, locale, status, platform_role, created_at, updated_at
FROM iam.users
WHERE id = $1;

-- name: InsertSession :exec
INSERT INTO iam.sessions (id, user_id, created_at, last_refreshed_at, revoked_at, revoke_reason)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: InsertRefreshToken :exec
INSERT INTO iam.refresh_tokens (token_hash, session_id, created_at, expires_at, used_at)
VALUES ($1, $2, $3, $4, $5);

-- name: RefreshTokenForUpdate :one
-- Locks the presented token and its session until the transaction ends, so
-- two refreshes of one session (or a refresh and a logout) take turns.
SELECT t.token_hash, t.session_id, t.created_at, t.expires_at, t.used_at,
       s.user_id, s.created_at AS session_created_at, s.last_refreshed_at,
       s.revoked_at, s.revoke_reason
FROM iam.refresh_tokens t
JOIN iam.sessions s ON s.id = t.session_id
WHERE t.token_hash = $1
FOR UPDATE OF t, s;

-- name: SessionForUpdate :one
SELECT id, user_id, created_at, last_refreshed_at, revoked_at, revoke_reason
FROM iam.sessions
WHERE id = $1
FOR UPDATE;

-- name: UpdateSession :exec
UPDATE iam.sessions
SET last_refreshed_at = $2, revoked_at = $3, revoke_reason = $4
WHERE id = $1;

-- name: MarkRefreshTokenUsed :exec
UPDATE iam.refresh_tokens
SET used_at = $2
WHERE token_hash = $1;
