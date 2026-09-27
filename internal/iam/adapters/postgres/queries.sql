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
