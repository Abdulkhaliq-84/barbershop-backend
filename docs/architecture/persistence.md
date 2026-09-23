# Persistence

PostgreSQL (with PostGIS, `btree_gist`, `pg_trgm`) is the only stateful dependency in v1 — data,
geo search, job queue and outbox all live there. Fewer moving parts to run on localhost and to learn.

## 1. Multi-tenancy

**Shared database, shared tables, `business_id` on every tenant-owned row**
([ADR-0004](../adr/0004-multi-tenancy-shared-schema.md)).

- The customer side is a *marketplace*: one query must search **all** tenants' branches on a map.
  With schema-per-tenant that becomes a UNION over N schemas, and every migration runs N times
  (and drifts). Shared tables make both trivial.
- Isolation is enforced in three layers:
  1. **API** — business-mode routes carry the tenant: `/v1/businesses/{businessId}/…`.
  2. **Application** — the use case checks the caller's membership in that business first.
  3. **Repository** — every tenant-scoped query takes `business_id` as a required parameter
     (`WHERE business_id = $1 AND …`); there is no "unscoped" repository method.
  4. **Database (M8)** — Row-Level Security policies keyed on `current_setting('app.business_id')`
     as defence in depth.

## 2. Schema per module (not per tenant)

Each bounded context owns a Postgres **schema** used as a namespace:

```
iam.users, iam.otp_challenges, iam.sessions
business.businesses, business.branches, business.staff_members, business.invitations
catalog.categories, catalog.services, catalog.service_offerings
scheduling.branch_calendars, scheduling.opening_hours, scheduling.closures,
scheduling.barber_schedules, scheduling.schedule_intervals, scheduling.time_off
booking.appointments, booking.appointment_items, booking.idempotency_keys
discovery.branch_listings, discovery.cities
billing.plans, billing.subscriptions
notification.device_tokens, notification.deliveries
media.objects
river.*   (job queue / outbox — managed by River's own migrations)
```

Rules: a module's SQL (sqlc `queries.sql`) only touches its own schema. No foreign keys across
schemas — cross-module references are plain UUID columns (the other module is the authority).

## 3. Key tables and constraints (sketch — final DDL lands with each milestone)

```sql
-- Extensions (M1)
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS btree_gist;  -- needed to mix "=" and "&&" in one EXCLUDE constraint
CREATE EXTENSION IF NOT EXISTS pg_trgm;     -- fuzzy text search

-- Double-booking guard (M5) — the core invariant lives in the database
CREATE TABLE booking.appointments (
    id              uuid PRIMARY KEY,               -- UUIDv7 generated in Go
    business_id     uuid        NOT NULL,
    branch_id       uuid        NOT NULL,
    barber_id       uuid        NOT NULL,
    customer_id     uuid        NOT NULL,
    status          text        NOT NULL,
    during          tstzrange   NOT NULL,           -- [start, end + buffer)
    total_price     bigint      NOT NULL,           -- halalas
    currency        char(3)     NOT NULL DEFAULT 'SAR',
    source          text        NOT NULL,
    version         int         NOT NULL DEFAULT 1, -- optimistic concurrency
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT no_overlapping_active_appointments
        EXCLUDE USING gist (barber_id WITH =, during WITH &&)
        WHERE (status IN ('pending', 'confirmed'))
);
CREATE INDEX ON booking.appointments (business_id, branch_id, lower(during));
CREATE INDEX ON booking.appointments (customer_id, lower(during) DESC);

-- Geo search (M6)
CREATE TABLE discovery.branch_listings (
    branch_id     uuid PRIMARY KEY,
    business_id   uuid NOT NULL,
    name_ar       text NOT NULL,
    name_en       text,
    search_text   text NOT NULL,                     -- normalised ar + en names
    city_code     text NOT NULL,
    location      geography(Point, 4326) NOT NULL,
    price_from    bigint,
    categories    text[] NOT NULL DEFAULT '{}',
    opening_hours jsonb NOT NULL,                    -- weekly, for "open now"
    updated_at    timestamptz NOT NULL
);
CREATE INDEX ON discovery.branch_listings USING gist (location);
CREATE INDEX ON discovery.branch_listings USING gin (search_text gin_trgm_ops);
CREATE INDEX ON discovery.branch_listings USING gin (categories);

-- Nearby query shape
-- SELECT … FROM discovery.branch_listings
-- WHERE ST_DWithin(location, ST_MakePoint($lng, $lat)::geography, $radius_m)
-- ORDER BY location <-> ST_MakePoint($lng, $lat)::geography
-- LIMIT $n;
```

## 4. Time

| Kind | Stored as | Example |
|---|---|---|
| Instant (something happens at one moment) | `timestamptz`, always UTC in Go | appointment start, created_at, time off |
| Recurring wall-clock (weekly template) | `weekday smallint`, `start_minute smallint`, `duration_minutes smallint` | "Thursday 16:00 for 10 h" (ends Fri 02:00) |
| Calendar date (branch-local) | `date` | closure from 2026-03-19 to 2026-03-22 |
| Timezone | IANA name on the branch | `Asia/Riyadh` |

- Go: `time.Time` for instants (convert with `.In(loc)` for display/logic), a small `WallClock` value
  object for templates. Load `time.Location` once per branch.
- The domain gets `now` from a `Clock` — tests can freeze time; no flaky "works except after 9 pm" bugs.
- Durations stored in minutes (smallint), represented as `time.Duration` in Go.

## 5. Money

`bigint` halalas (1 SAR = 100 halalas) + `char(3)` currency. Consumer prices are **VAT-inclusive**
(Saudi display rule); VAT breakdown is computed only where invoices need it (later, with ZATCA).

## 6. IDs

UUIDv7 generated in Go (`uuid.NewV7()`): globally unique, time-ordered (good B-tree locality),
safe to expose in URLs, and creatable before insert (useful for idempotency and events).

## 7. Migrations

- goose, plain SQL files in `migrations/`, one global ordered sequence (modules share one DB).
- **Forward-only once merged** — never edit a merged migration; add a new one.
- Every migration runs in CI against a fresh PostGIS container; destructive changes follow
  expand → migrate → contract across releases.
- River ships its own migrations; we run them via its migrator at the same step.
