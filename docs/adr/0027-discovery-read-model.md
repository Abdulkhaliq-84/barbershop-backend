# ADR-0027: Discovery keeps its own copy of each branch, from events that carry the branch

- Status: Accepted · Date: 2026-10-01 · Builds on [ADR-0005](0005-postgres-postgis.md), [ADR-0009](0009-domain-events-outbox-river.md) and [ADR-0019](0019-outbox-delivery-and-billing-trial.md)

## Context
M6 lets customers find branches: by city, near them, by name, by category, open now. The
answers come from three modules: business (who and where), catalog (what and for how much)
and scheduling (when open). Searching them directly would mean:

- joins across module schemas, which the architecture forbids (ADR-0015);
- or a request fanning out to three modules for every search.

Either way, searches would slow down the screens owners use to run their shops. Search also
wants its own shape: a geography column, trigram indexes, the price from, the categories.

M6.1 starts with business: the branches, and browsing them by city.

## Decision
- **A read model in its own module.** `discovery` keeps a copy of each branch in
  `discovery.branch_listings`. It is built only from events, which main subscribes it to.
  - discovery depends on no other module, and no module depends on it (lint rules).
  - Nothing outside discovery reads its tables, and no API request writes them.
- **Events carry the branch.** Every branch event carries the branch as of its version
  (`events.Branch`): name, city, district, address, location, phone, time zone and status.
  - `business.branch_published` and `business.branch_unpublished` gain it (fields added, nothing
    removed, so the contract stays compatible).
  - A new `business.branch_updated` is recorded whenever the owner edits a branch.
  - Event-carried state means discovery never calls back into business, and a copy is right
    after any one event.
- **The newest version wins.** Events can arrive twice and out of order (at-least-once
  delivery, retries). The upsert writes only if the stored copy's version is older:
  `ON CONFLICT … DO UPDATE … WHERE version < excluded.version`.
  - An unpublished branch keeps its row (`listed = false`). So a late, older "updated" event
    can't bring it back.
  - The check and the write are one statement, so two events handled at once can't leave the
    older one.
- **Cities are reference data in code** (`shared.Cities`), like catalog's categories
  (ADR-0020), not a `discovery.cities` table.
  - business accepts a branch only in a city on the list.
  - Customers get the list from `GET /v1/cities`, and browse with `GET /v1/branches?city=`.
  - The list lives in the shared kernel because two modules need exactly the same codes.
- **Browsing a city:** listed branches by Arabic name in code point order (`COLLATE "C"`), then
  ID, 20 a page, with an opaque keyset cursor. "C" order is the same on every machine, so a
  cursor means the same in development, CI and production.
- **Public:** discovery shows only what owners published. Whether a branch can really be
  booked is still checked when booking (ADR-0024), so a copy a moment old is harmless.

## Consequences
- A change shows in search a moment after it is made: the outbox worker delivers it.
- Branches published before M6.1 have no copy until they next change. Nothing is live yet, so
  there is no backfill. Once something is, a "resend every branch" task would rebuild the copies.
- A future way of hiding branches must publish a branch event, or discovery will keep listing
  them. Two are known: suspending a business (the M3 exercise) and billing unpublishing
  branches over a plan's limit.
- Later M6 slices add columns to the same table, fed the same way:
  - the location as `geography` (nearby);
  - normalised names (text search);
  - catalog's and scheduling's events (categories, price, open now).
