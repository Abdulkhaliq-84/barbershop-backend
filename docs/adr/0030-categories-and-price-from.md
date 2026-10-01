# ADR-0030: Categories and price from — catalog's events, a copy per service, answered at search time

- Status: Accepted · Date: 2026-10-01 · Builds on [ADR-0009](0009-domain-events-outbox-river.md), [ADR-0020](0020-catalog-and-cross-module-authorization.md) and [ADR-0027](0027-discovery-read-model.md)

## Context
M6.4 lets a customer narrow a search to a kind of service ("beard trims near me") and shows
what a branch costs ("from 35 SAR"). Both answers belong to catalog: what each branch sells,
of which category, at what price, and who performs it. Discovery must answer them without
reading catalog's tables (ADR-0027).

Two things make it more than "add two columns":

- A branch has many services, each its own aggregate with its own version. No single event
  holds "the branch's menu".
- A branch's services exist before the branch is published, so their events usually arrive
  before discovery has a copy of the branch at all.

## Decision
- **Catalog publishes service events.** `catalog.service_created` and
  `catalog.service_updated` (details, active or not, or who performs it) carry the service as
  of its version: category, name, duration, price, active.
  - They also carry `price_from`: the least a customer pays for it, the cheapest performer's
    price. It is absent while nobody performs it. The rule stays in catalog
    (`ServiceSnapshot.PriceFrom`); subscribers don't re-derive it.
  - The service records its events and the repository publishes them in the change's own
    transaction. Adding a service is now a transaction too.
  - `catalog.OnServiceChanged` decodes them for subscribers. A service is *offered* when it is
    active and someone performs it.
- **Discovery keeps a copy per service** in `discovery.branch_services`: offered or not, its
  category, its price from. The newest version wins, as for branches (`ON CONFLICT … WHERE
  version < excluded.version`).
  - The table has no link to `branch_listings`: services and branches arrive in any order.
- **Answered at search time, not stored on the listing.** Every search joins the two inside
  discovery's own schema:
  - `EXISTS` an offered service (of the category, if given) filters the branches;
  - a subquery takes the least `price_from` for the branches on the page.
  - One index, `(branch_id, category_code, price_from) WHERE offered`, answers both from the
    index alone. With a category, the least price is its first entry.
  - Keeping categories and the price from on each listing instead would need recomputing them
    on every service event, under a lock per branch. Two services of one branch changing at
    once could otherwise each miss the other's change. Answering at search time has no such
    race.
- **A branch is found only while it offers something.** Every search, with a category or not,
  shows only branches with an offered service. A branch whose services are all turned off
  can't be booked, so it isn't shown. So every listing has a price from, and the API always
  sends `price_from`.
- **Categories move to the shared kernel** (`shared.Categories`), like cities (ADR-0027).
  Catalog files services under them and discovery checks the `category` parameter against
  them. An unknown one is `422 unknown_category`.
- **The API:** `GET /v1/branches?category=` narrows any search (city, place or name).
  `price_from` is with each branch: the least a service there costs, for one of the
  category's if given. Neither changes a search's order or its cursor.

## Plan and timing
22,000 branches, 176,000 services (8 a branch, 1 in 10 not offered), category `kids`:

| Search | With the index | Without it |
|---|---|---|
| A city's branches | 0.7 ms: a nested-loop semi join reading 33 listings to fill 21 | 3.2 s |
| Near a place | 19 ms: 662 candidates from the GiST index, one index probe each | 311 ms |
| By name | 27–45 ms: 1,221 trigram candidates, one probe each | — |

Without the index, each probe and each price is a scan of the 176,000 services. A test checks
the plan reads the index.

## Consequences
- A change to a service shows in search a moment later, like a branch's.
- "Offered" means active with a performer. Catalog doesn't know when a barber leaves the
  business, so a service whose only barber left still counts until it is changed. Booking
  still checks everything at the time (ADR-0024).
- Services created before M6.4 have no copy until they next change. Nothing is live, so there
  is no backfill (as in ADR-0027).
- Prices are SAR only. Discovery refuses another currency (`ErrNotSAR`) rather than compare
  amounts in different currencies.
- Sorting by price (the exercise) orders by the same subquery. A deep page would then compute
  every candidate's price first; a column kept on the listing would avoid that, at the cost
  above.
