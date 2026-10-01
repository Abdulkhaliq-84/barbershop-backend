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
iam.users, iam.otp_challenges, iam.otp_phone_guards, iam.sessions, iam.refresh_tokens
business.businesses, business.branches, business.staff_members, business.staff_branches,
business.verification_documents, business.invitations
catalog.services, catalog.service_offerings  (categories are reference data in shared — ADR-0030)
scheduling.branch_calendars, scheduling.opening_hours, scheduling.closures (the owner's exercise),
scheduling.barber_schedules, scheduling.barber_hours, scheduling.schedule_overrides,
scheduling.override_hours, scheduling.time_off (EXCLUDE: no overlapping time off per person)
booking.appointments, booking.appointment_items, booking.idempotency_keys
discovery.branch_listings, discovery.branch_services, discovery.branch_hours  (cities are reference data in code — ADR-0027)
billing.subscriptions  (plans are reference data in code — ADR-0019)
notification.device_tokens, notification.deliveries
media.objects
river.*   (job queue / outbox — created and upgraded by River's own migrator, run by database.Migrate)
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

-- Search (M6): copies kept from events, the newest version winning (ADR-0027–0031)
CREATE TABLE discovery.branch_listings (
    branch_id     uuid PRIMARY KEY,
    business_id   uuid NOT NULL,
    version       integer NOT NULL,
    listed        boolean NOT NULL,                  -- published
    name_ar       text COLLATE "C" NOT NULL,
    name_en       text NOT NULL,
    search_text   text NOT NULL,                     -- normalised ar + en names
    city_code     text NOT NULL,
    latitude, longitude double precision NOT NULL,
    location      geography(Point, 4326) GENERATED ALWAYS AS (…) STORED,
    timezone      text NOT NULL,                     -- IANA; "open now" is in it
    updated_at    timestamptz NOT NULL
);
CREATE INDEX ON discovery.branch_listings (city_code, name_ar, branch_id) WHERE listed;
CREATE INDEX ON discovery.branch_listings USING gist (location) WHERE listed;
CREATE INDEX ON discovery.branch_listings USING gin (search_text gin_trgm_ops) WHERE listed;

-- What each branch sells: categories and the price from are answered at
-- search time (EXISTS, min) from this table's index, not stored per listing.
CREATE TABLE discovery.branch_services (
    service_id    uuid PRIMARY KEY,
    branch_id     uuid NOT NULL,
    version       integer NOT NULL,
    offered       boolean NOT NULL,                  -- active, and someone performs it
    category_code text NOT NULL,
    price_from    bigint NOT NULL                    -- the cheapest performer's, in halalas
);
CREATE INDEX ON discovery.branch_services (branch_id, category_code, price_from) WHERE offered;

-- When each branch is open: "open now" is computed per search, by
-- discovery.open_at(open, at, timezone), in the branch's own time zone.
CREATE TABLE discovery.branch_hours (
    branch_id     uuid PRIMARY KEY,
    version       integer NOT NULL,                  -- the calendar's
    open          int4multirange NOT NULL            -- minutes after Sunday 00:00, local
);

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
- Down migrations never drop **extensions** (shared infrastructure that may predate the migration;
  dropping `postgis` would destroy geography columns).
- Every migration runs in CI against a fresh PostGIS container; destructive changes follow
  expand → migrate → contract across releases.
- River ships its own migrations; we run them via its migrator at the same step.

## 8. Connections and timeouts

- One pool per process, `DATABASE_MAX_CONNS` connections (at least 2). In the worker, River keeps
  4 for itself (LISTEN, fetching, completing, leader duties); jobs run on the rest, at most 10 at a
  time.
- The api and worker roles set three limits on every connection, which Postgres enforces
  whatever the Go code does:

  | Setting | Default | Stops |
  |---|---|---|
  | `statement_timeout` | 10 s | a slow query holding a connection |
  | `lock_timeout` | 5 s | a wait for a row or advisory lock (`FOR UPDATE`, `pg_advisory_xact_lock`) |
  | `idle_in_transaction_session_timeout` | 30 s | a transaction left open; its locks go with the session |

  The request's context isn't enough on its own: net/http cancels it when the client goes away,
  not when the server's write timeout passes. No transaction may span a file transfer or another
  network call.
- `server migrate` runs without these limits: a migration may rewrite a table or wait for traffic
  to let go of one.
