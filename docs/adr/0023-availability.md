# ADR-0023: Availability is a pure calculation over three modules, public and advisory

- Status: Accepted · Date: 2026-09-30 · Builds on [ADR-0008](0008-double-booking-exclusion-constraint.md), [ADR-0021](0021-scheduling-time-model.md) and [ADR-0022](0022-branch-publishing-and-readiness.md)

## Context
M5.2 answers the customer's first question: "when can this branch do these services on this day?"
The answer needs four sources:
- `business`: the branch (published, of an active business), its time zone and booking policy, and
  which barbers still work there;
- `catalog`: who performs each service, for how long and at what price;
- `scheduling`: each barber's working windows;
- `booking` itself: their appointments.

## Decision
- **A new `booking` module** combines them through `adapters/acl`, the same shape as catalog and
  scheduling (ADR-0020). Lint rules keep it to the three root packages, and keep them from
  depending on booking.
- **The calculation is a pure function**, `domain.Slots(day, rules, candidates)`. A start time `t`
  is offered for a barber when:
  - `[t, t + their duration + the branch's buffer)` lies inside one of their working windows;
  - it overlaps none of their active appointments (stored with their own buffer);
  - `t` is on the clock grid (every `slot_interval` minutes after midnight) and within
    `[now + min lead, end of today + horizon)`.
  It is tested against a minute-by-minute oracle in four time zones (including the daylight-saving
  days), fuzzed, and benchmarked: about 0.1 ms for a busy day with eight barbers.
- **The grid walks real instants, not clock times.** `time.Date` on a repeated wall-clock time
  (clocks going back) picks one of the two instants, and which one is not guaranteed: in our tests
  London and New York differ. So the grid visits every minute of the day and keeps those whose
  local clock is on the grid. In the repeated hour both instants are offered.
- **Several services, one barber, back to back**: the candidates are the barbers who perform all
  of them. Their duration and price are the sums, using their own overrides.
- **Windows and appointments load at the same time** (`errgroup`). They come from different
  modules and neither needs the other. The window reaches past midnight by the longest
  candidate's length, so a late start that ends after midnight still fits.
- **Public and advisory**: `GET /v1/branches/{id}/availability` needs no token. A published
  branch's free times aren't secret, and customers browse before signing in. A slot may be taken
  before the customer books it; the exclusion constraint decides (`409 slot_unavailable`, M5.3).
- **The appointments table arrives now**, complete, because availability reads it:
  - `during` is `[starts_at, ends_at + buffer)`;
  - `EXCLUDE USING gist (staff_id WITH =, during WITH &&) WHERE status IN ('pending','confirmed')`;
  - items are snapshots of name, duration and price.

## Consequences
- An unknown day format is `400`. A day before today or past the horizon has no slots rather than
  an error, so the app just shows an empty day.
- A barber who left the branch between two of the reads shows no free times for that request,
  rather than failing it.
- Rate limiting of this public endpoint arrives with M8 (hardening), like search.
