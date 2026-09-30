# ADR-0018: Staff invitations — a token sent to a phone, accepted by that phone, branch-scoped managers

- Status: Accepted · Date: 2026-09-30 · Builds on [ADR-0015](0015-business-tenancy-and-authorization.md)

## Context
M3.5 lets an owner bring their team in: managers and barbers. A barber may not have the app yet, so
the invitation must reach them by SMS and turn into a staff membership when they sign in. Managers
run some branches, not the whole business, so authorization must now look at branches, not only
roles.

## Decision
- **Invite by phone, with a secret link.**
  - The owner invites a phone number, a name, a role (`manager` or `barber`; never `owner`) and
    1–50 branches of their own business.
  - The server makes a random 256-bit token (`inv_` + base64url) and texts the deep link
    `barbershop://invitations/accept?token=…` to that phone.
  - Only the SHA-256 of the token is stored. A plain hash is enough because the token is random:
    there is nothing to guess. A leaked database can't be used to join a shop.
  - The token is never returned by the API: only the invited phone receives it.
- **The token alone is not enough.**
  - Accepting needs the token *and* a caller signed in with the invited phone. `business` asks
    `iam` for the caller's phone through its root package (`iam.Module.PhoneOf`, behind the
    `acl` adapter), never through `iam`'s tables.
  - A forwarded or overseen SMS is useless to anyone else.
  - Every refusal gives the same answer, `404 invitation_invalid`: unknown token, wrong phone,
    expired, revoked or already used. The answer never tells which one it was.
- **The token goes in the body** (`POST /v1/invitations/accept {token}`), not the URL as first
  planned. Paths are written to the request log; bodies are not.
- **One pending invitation per phone per business** (partial unique index).
  - Inviting again revokes the old one and sends a new link. That is how an owner resends.
  - Invites of one business take turns under a `FOR NO KEY UPDATE` lock on the business row.
    This lock mode doesn't block rows that merely reference the business.
  - Invitations expire after 7 days. Expiry is computed, not stored: a pending invitation past
    `expires_at` can't be accepted.
- **Accepting is one transaction.**
  - It locks the invitation (`FOR UPDATE`), marks it accepted, and inserts the staff member and
    their branches.
  - A double-tapped "accept" takes turns: the second sees it used.
  - Someone already on the staff (for example, the owner) gets `409 already_staff`.
- **Branches belong to staff.** `staff_branches` has foreign keys on `(business_id, staff_id)`
  and `(business_id, branch_id)`. The database itself refuses to put one shop's barber in another
  shop's branch.
- **Branch-scoped authorization.**
  - `StaffMember.AuthorizeBranch(role, branch)` is the role check plus "works at this branch".
    The owner works at every branch.
  - Editing a branch now needs a manager of that branch, or the owner. Only the owner changes a
    branch's time zone (*changed after review, M4*): opening hours and schedules are wall-clock
    times in it, so all of them move with it.
  - Creating branches, inviting, and reading or editing the business stay owner-only.
  - Listing the team is for managers and up.
- **Delivery.** The SMS is sent after the transaction commits, so a failed save never texts a
  dead link. If sending fails, the owner gets an error and invites again (which revokes the first
  invitation).
  - In development the link is written to the log by the same `SMS_PROVIDER=console` mode as
    login codes, with the phone masked. The config refuses that mode in production.
  - Real SMS arrives with notifications (M7). Retries arrive with the outbox (M3.6).
- **Sending limits** (*changed after review, M4*). Every invitation is a paid SMS to someone who
  didn't ask for it, and drafts may invite, so without limits one account could text a stranger
  without end (SMS pumping, harassment).
  - One a minute to a phone from a business; 5 a day to a phone from all businesses together;
    50 a day from a business. Over a limit: `429 rate_limited` with `Retry-After`.
  - They are checked in the invite transaction, under the business lock and an advisory lock on
    the phone, so parallel invites from different businesses can't all get through.
  - A user has at most 3 businesses not yet approved (`409 registration_limit_reached`), so
    fresh drafts don't multiply the per-business limit.

## Consequences
- Inviting a number that already works at the business only fails when it is accepted
  (`409 already_staff`): `business` doesn't look users up by phone.
- Invitations may be sent while the business is still a draft, so the team is ready at launch.
  Plan limits on barbers arrive with billing (M3.6).
- Removing staff, changing their branches and deactivating them come later (M4 staff
  management). `active` already exists and is respected by every check.
- Barbers get their own schedule and bookings in M4/M5. `AuthorizeBranch` is the check those use
  cases will reuse.
