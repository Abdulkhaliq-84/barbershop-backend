# ADR-0017: Business review — admin access from the token, keyset pagination, outbox with its first consumer

- Status: Accepted · Date: 2026-09-30 · Builds on [ADR-0014](0014-sessions-and-access-tokens.md) and [ADR-0015](0015-business-tenancy-and-authorization.md)

## Context
M3.4 lets an owner submit a business and a platform admin approve or reject it. This is the first
platform-admin API and the first paginated list. The plan also scheduled the River outbox and the
`worker` role for this slice, so domain events (`BusinessApproved`…) can reach other modules.

## Decision
- **Admin access comes from the access token's `platform_role`** (ADR-0014). No database call is
  made per request.
  - Every admin use case checks it first. Non-admins get `403 forbidden`: admin routes are
    well known, so there is nothing to hide.
  - Removing the role takes effect when the token expires (≤ 15 minutes). A refresh after
    promotion carries the new role.
  - There is no API to create admins. An operator sets `iam.users.platform_role = 'admin'`.
- **Nobody reviews their own business**, admin or not (`403`). *Changed after review (M4):* nor a
  business they work at or used to work at. Any staff record, active or not, makes an admin not
  independent.
- **Submission is checked under the business row lock.** The CR document and branch counts are
  read in the same transaction, and attaching a document takes the same lock. The CR number is
  claimed platform-wide by the partial unique index from ADR-0015. A clash returns
  `409 cr_number_taken`, and rejecting releases the claim.
- **Decisions use the same `If-Match` version check** as edits. Two admins deciding at once
  can't both win: the second gets `412`, or `409` once the status has moved.
- **Lists use keyset pagination.**
  - The client passes `?limit=` and an opaque `cursor`; the response carries `next_cursor`.
  - The cursor is base64url of the last row's sort key (`submitted_at|id`). The next page is
    "rows after this key", never `OFFSET`, so pages don't shift when rows are added or removed
    between requests.
  - The handler fetches `limit + 1` rows to know whether another page exists, without a
    `COUNT(*)`.
  - Cursors are not signed: they only say where to continue, and every page is still filtered
    and authorized.
- **The River outbox moves to M3.6**, together with its first consumer: billing starting a trial
  on `BusinessApproved`. An outbox with no subscriber would be infrastructure without a test that
  proves it works end to end. Until then the state change itself is the record (`submitted_at`,
  `reviewed_at`, `reviewed_by`, `rejection_reason`).

## Consequences
- An admin whose role is removed keeps admin access for up to 15 minutes. If that ever matters,
  admin use cases can re-check the role in `iam` through its root package.
- Every future list endpoint follows the same cursor shape (`limit`, `cursor`, `next_cursor`).
- M3.6 must add the outbox before anything else listens for business events. `ADR-0009` still
  holds; only the timing moved.
