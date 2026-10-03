# ADR-0031: Open now — scheduling's events, the week as a multirange, each branch's own time zone

- Status: Accepted · Date: 2026-10-01 · Builds on [ADR-0021](0021-scheduling-time-model.md), [ADR-0027](0027-discovery-read-model.md) and [ADR-0030](0030-categories-and-price-from.md)

## Context
M6.5 tells a customer which branches are open at this moment, and lets them see only those.
Scheduling owns the opening hours (ADR-0021): a week of intervals in the branch's own
wall-clock time, some running past midnight (Thursday 16:00 to Friday 02:00), one even past
Saturday midnight into Sunday. Discovery must answer without reading scheduling's tables
(ADR-0027).

Two things make "open now" harder than a filter on a column:

- **It depends on the moment asked.** The same branch is open at noon and closed at 3 am, so
  the answer can't be stored. It has to be computed when someone searches.
- **It depends on each branch's time zone.** "09:00–21:00" in Riyadh and in Dubai are
  different instants. The app serves one country today, but the copy keeps each branch's
  zone (business's events carry it), so nothing assumes one.

## Decision
- **Scheduling publishes `scheduling.opening_hours_changed`** whenever an owner or manager
  sets the hours. It carries the whole week as of the calendar's version: each interval's
  weekday, start minute and length, in branch-local time.
  - It is published in the change's own transaction, like business's and catalog's events.
  - `scheduling.OnOpeningHoursChanged` decodes it and checks each interval with the domain's
    own rules (`NewWeeklyHours`). It hands subscribers the week as `[start, end)` minutes
    after Sunday 00:00.
- **Discovery keeps the week as an `int4multirange`** in `discovery.branch_hours`, one row
  per branch, the newest calendar version winning. Sunday 09:00–21:00 is `[540,1260)`.
  Saturday 22:00 to Sunday 02:00 is `[9960,10200)`, past the week's 10,080 minutes.
  - A multirange is a set of ranges in one value. "Does the week contain this minute?" is
    one operator, `@>`, and an empty week is "closed".
  - It is its own table, not columns on the listing: the calendar has its own version, and
    its event often arrives before the branch is published.
- **One SQL function decides "open"**: `discovery.open_at(open, at, tz)`.
  - It converts the instant `at` to the branch's time zone (`at AT TIME ZONE tz`).
  - It takes the minute of the week that falls on there.
  - It checks the week contains that minute, or that minute plus a week (for an interval past
    Saturday midnight).
  - Opening at 09:00 is open from 09:00:00; closing at 21:00 is closed from 21:00:00.
- **The instant comes from Go.** The use case passes `clock.Now()` as `at`, not SQL's
  `now()`. So tests set the clock, and a search answers for one moment throughout.
- **Every search says `open_now`, and `open_now=true` keeps only the open.**
  - The hours are joined by primary key (`LEFT JOIN`).
  - A branch with no hours copy yet is not open.
  - Order, pages and cursors are unchanged.

## Plan and timing
22,000 branches, 19,700 with hours (8 in 10 open 09:00–21:00, the rest 16:00–02:00), at
Thursday 23:00 in Riyadh:

| Search | Time | How |
|---|---|---|
| A city's branches, open now | 3–4 ms | city index in name order; 116 listings checked to fill 21 |
| Near a place, open now | 20–30 ms | 662 GiST candidates, each checked |
| A city's branches, any | 1.3–1.8 ms | as before, plus one primary-key probe a row |
| A city's branches, open now, at 04:00 (none open) | 30 ms | all 2,864 Riyadh listings checked |

No index can answer "open now". The minute to look up differs by time zone, and it changes
every minute. So the cost is one check per candidate. A city is small enough that checking
all of it, in the worst case, is 30 ms.

## Consequences
- "Open now" is by weekly hours only. Closures (holidays, the M4.3 exercise) aren't known to
  discovery until they publish an event too.
- A change to the hours shows in search a moment later, like every other change.
- Branches whose hours were set before M6.5 have no copy until they next change. They show as
  closed. Nothing is live, so there is no backfill (as in ADR-0027).
- The public branch page (M6.6) can show the week from the same copy.
