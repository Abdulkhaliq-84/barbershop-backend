# ADR-0021: Scheduling keeps weekly hours in branch-local wall-clock time, with shifts past midnight

- Status: Accepted · Date: 2026-09-30 · Builds on [ADR-0020](0020-catalog-and-cross-module-authorization.md)

## Context
M4.3–M4.5 add the `scheduling` module:
- when a branch is open;
- when each barber works;
- time off;
- the calculation that turns these into concrete working windows for booking (M5).

Barbershops in the Gulf often work split shifts and stay open past midnight, especially on Thursday
and Friday nights. Branches keep their own IANA time zone (M3.2).

## Decision
- **Weekly hours are branch-local wall-clock minutes**, not instants:
  `(weekday, start minute, length)`. "We open at 4 pm" must stay 4 pm whatever the UTC offset,
  including in zones with daylight saving time.
- **An interval may run past midnight and belongs to the day it starts on.** Thursday
  `16:00–02:00` is one ten-hour Thursday shift, not two pieces.
  - The API speaks `opens`/`closes`; a `closes` at or before `opens` is on the next day.
    `00:00–00:00` or `00:00–24:00` is all day.
  - Rules: 5-minute steps, 5 minutes to 24 hours long, at most 4 intervals starting per day.
  - No overlaps, including a Saturday-night shift running into Sunday (the week wraps).
  - One value object, `WeeklyHours`, holds all this for branch hours now and barber templates
    in M4.4.
- **A calendar is versioned like everything else.** It is version `0` until first saved:
  `GET` returns a closed week with version 0, and the first `PUT` sends `If-Match: 0`. Two
  first saves at once both see no row; the second insert hits the primary key, which is reported
  as `412 version_conflict`.
- **Who:** anyone working at the branch reads its hours; the owner or a manager of the branch
  sets them (`business.AuthorizeBranch`, ADR-0020).
- **Instants are computed per date**, in M4.5: an interval on a date becomes
  `time.Date(y, m, d, 0, start, 0, 0, loc)` to `time.Date(y, m, d, 0, start+length, 0, 0, loc)`.
  Go normalizes minutes past 24:00 into the next day in wall-clock terms. Time off and
  appointments are instants (`timestamptz`).
- `scheduling` depends only on `business` (through its root package). It must not import
  `catalog`: booking combines the two.

## Consequences
- In a DST zone, a shift crossing the switch lasts an hour more or less in real time. That is
  what people expect ("we close at 2 am"); Riyadh has no DST.
- Closures (holidays, renovation) are the owner's exercise. They belong to the branch calendar
  and remove whole dates from the working windows.
