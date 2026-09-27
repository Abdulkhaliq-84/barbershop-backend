# ADR-0013: Serialize OTP operations and retain phone-level failures

Status: accepted

## Context

Separate reads of the latest challenge and hourly count let concurrent requests
bypass both limits. A new challenge also resets its individual guess allowance.

## Decision

Both request and verify run in a READ COMMITTED transaction with a PostgreSQL
transaction advisory lock keyed by the canonical E.164 phone (hashtextextended,
IAM-specific seed). It protects even the first request, when no row exists.
Every repository call within the callback uses that transaction's connection.
Hash collisions can delay unrelated phones but cannot permit extra attempts.
Application policy stays in IAM; SQL and transaction management stay in its adapter.

A new `iam.otp_phone_guards` table counts failed verifications across challenges.
Ten failures within a 15-minute fixed window lock both request and verify for
15 minutes. Unknown, expired, consumed and exhausted challenges count too.
Successful verification clears failures; resending never does. Attempts during
lockout do not extend it. Invalid request syntax is rejected before verification
and does not consume a code comparison. Existing per-code five-attempt limits,
one-minute resend cooldown and five-per-hour cap remain.

Business failures are returned after committing counters; infrastructure failures
roll back. Clock reads occur after acquiring the lock. SMS delivery occurs after
commit, so provider latency cannot hold the lock. A failed SMS delivery still uses
a request slot, as before; reliable delivery/outbox work belongs to M7.

## Consequences

Per-phone serialization works across processes. A focused concurrency test pauses
code generation after policy reads, so removing the advisory lock lets a competing
request through. Burst tests and parallel verification tests cover persisted limits.

Lockouts can be abused to temporarily deny another phone access. The bounded,
non-extending deadline limits this cost; distributed/IP abuse controls remain
future work. Failure rows need a retention/cleanup policy before production. The
local database still uses one development superuser; production runtime/migration
role separation is a deployment prerequisite, outside this focused patch.
