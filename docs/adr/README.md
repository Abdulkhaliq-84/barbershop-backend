# Architecture Decision Records

Short documents capturing *why* a significant decision was made. Format: Context → Decision →
Consequences. An ADR is never edited after acceptance — a new ADR supersedes it.

| # | Decision | Status |
|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | Accepted |
| [0002](0002-modular-monolith.md) | DDD modular monolith | Accepted |
| [0003](0003-go-stack.md) | Idiomatic Go stack: net/http + chi, pgx + sqlc, goose | Accepted (test tooling amended by 0012) |
| [0004](0004-multi-tenancy-shared-schema.md) | Shared-schema multi-tenancy with `business_id` | Accepted |
| [0005](0005-postgres-postgis.md) | PostgreSQL + PostGIS as the only stateful dependency | Accepted |
| [0006](0006-openapi-first.md) | OpenAPI-first REST contract | Accepted |
| [0007](0007-auth-phone-otp-jwt.md) | Own auth: phone OTP + JWT + rotating refresh tokens | Accepted |
| [0008](0008-double-booking-exclusion-constraint.md) | Prevent double booking with a Postgres exclusion constraint | Accepted |
| [0009](0009-domain-events-outbox-river.md) | Domain events via transactional outbox on River | Accepted |
| [0010](0010-pay-at-shop-first.md) | Pay at the shop in v1 | Accepted |
| [0011](0011-ci-cd-github-actions.md) | CI/CD on GitHub Actions — build once, publish versioned images, deploy later | Accepted |
| [0012](0012-test-database-per-test.md) | Integration tests get a throwaway database on a shared PostGIS server | Accepted |
| [0013](0013-otp-phone-security.md) | Serialize OTP operations and keep phone-level failures | Accepted |
| [0014](0014-sessions-and-access-tokens.md) | Stateless access tokens, rotating refresh sessions, auth declared in the API spec | Accepted |
| [0015](0015-business-tenancy-and-authorization.md) | Business tenancy: membership-first authorization, CR claimed on submission, no cross-module foreign keys | Accepted |
| [0016](0016-media-storage-and-signed-links.md) | Media: files on a storage port, private files through signed links | Accepted |
| [0017](0017-business-review-and-admin-access.md) | Business review: admin access from the token, keyset pagination, outbox with its first consumer | Accepted |
| [0018](0018-staff-invitations.md) | Staff invitations: a token sent to a phone, accepted by that phone, branch-scoped managers | Accepted |
| [0019](0019-outbox-delivery-and-billing-trial.md) | Outbox delivery per subscriber, a worker role, billing limits checked when adding | Accepted |
| [0020](0020-catalog-and-cross-module-authorization.md) | Catalog as its own module; other modules ask business who may work on a branch | Accepted |
| [0021](0021-scheduling-time-model.md) | Scheduling keeps weekly hours in branch-local wall-clock time, with shifts past midnight | Accepted |
| [0022](0022-branch-publishing-and-readiness.md) | Publishing a branch asks catalog and scheduling through main | Accepted |
| [0023](0023-availability.md) | Availability is a pure calculation over three modules, public and advisory | Accepted |
| [0024](0024-booking-an-appointment.md) | Booking: the key first, the database decides, one lock per barber shared with scheduling | Accepted |
| [0025](0025-appointment-lifecycle.md) | Appointment lifecycle: the domain decides, one change at a time, the deadline kept with the booking | Accepted |
| [0026](0026-walk-ins-and-pending-expiry.md) | Walk-ins and pending expiry: the shop books by name, a scheduled task expires what wasn't answered | Accepted |
| [0027](0027-discovery-read-model.md) | Discovery keeps its own copy of each branch, from events that carry the branch | Accepted |
| [0028](0028-nearby-search.md) | Nearby search: a generated geography column, one distance everywhere, pages by distance | Accepted |
| [0029](0029-search-by-name.md) | Search by name: normalise in Go, match with trigrams, a lower threshold | Accepted |
| [0030](0030-categories-and-price-from.md) | Categories and price from: catalog's events, a copy per service, answered at search time | Accepted |
| [0031](0031-open-now.md) | Open now: scheduling's events, the week as a multirange, each branch's own time zone | Accepted |
| [0032](0032-branch-page.md) | The branch's public page: one snapshot of discovery's copies, shown only if search would show it | Accepted |
| [0033](0033-notifications-devices-and-pushes.md) | Notifications: devices per user, pushes from booking's events, sent once per device | Accepted |
| [0034](0034-fcm-sender.md) | Pushes through FCM: a service account signs in, bounded retries, three kinds of failure | Accepted |
