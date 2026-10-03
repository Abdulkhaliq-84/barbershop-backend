# ADR-0032: The branch's public page — one snapshot of discovery's copies, shown only if search would show it

- Status: Accepted · Date: 2026-10-03 · Builds on [ADR-0027](0027-discovery-read-model.md), [ADR-0030](0030-categories-and-price-from.md) and [ADR-0031](0031-open-now.md)

## Context
A customer who taps a branch in search needs its page before booking:

- where it is and how to call it;
- its weekly hours, and whether it is open now;
- its menu: the services they can choose, from how much, and the IDs that availability
  (ADR-0023) and booking take.

Everything on it comes from three modules: business (the branch), scheduling (the hours) and
catalog (the services). Discovery already keeps a copy of each part (ADR-0027, -0030, -0031),
but two pieces were missing:

- **The services' names, durations and order.** Discovery's copy held only what searching
  needed: the category and the price from.
- **The week as the owner set it.** The `int4multirange` from M6.5 merges back-to-back ranges:
  seven all-day intervals become one week-long range. That's right for "open now", but the
  page must show the week day by day.

## Decision
- **Catalog's service events add `sort_order`** (a field added, nothing removed). They already
  carried the name and duration. `catalog.ServiceChanged` passes all three on, and discovery's
  service copy keeps them (`name_ar`, `name_en`, `duration_minutes`, `sort_order`).
- **The hours' copy also keeps the intervals as set** (`intervals`, JSON pairs of minutes after
  Sunday 00:00). It sits next to the multirange, written from the same value in the same
  statement. One shape is for checking, the other for showing.
- **`GET /v1/branches/{branch_id}`, public**, answers:
  - the listing, with the price from and open now, as search shows it;
  - the week in the owners' own shape: seven days, Sunday first, `opens`/`closes`;
  - the menu, by the shop's order, then Arabic name in code point order, then ID.
- **One snapshot.** The page and its menu are two queries in one repeatable-read, read-only
  transaction. A service event landing between them can't make the price from disagree with
  the menu.
- **Shown only if search would show it.** The branch must be published and offer a service,
  the rule ADR-0030 set for search. Otherwise the answer is `404`, as for an ID that doesn't
  exist, so a page never shows a branch customers can't find or book.

## Consequences
- The menu's duration is the service's own. A barber may take longer; availability says how
  long with each.
- Barbers and photos aren't on the page yet. Barbers' public names would come from business's
  staff events; photos from media (later milestones).
- Copies saved before M6.6 show empty names and durations, and no week, until their next
  event. Nothing is live, so there is no backfill (as in ADR-0027).
- The page is a few index lookups on primary keys and the services index: one branch, its hours
  row, its services.
