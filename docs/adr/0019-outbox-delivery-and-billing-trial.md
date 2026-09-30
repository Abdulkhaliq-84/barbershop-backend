# ADR-0019: Outbox delivery per subscriber, a worker role, and billing limits

- Status: Accepted · Date: 2026-09-30 · Implements [ADR-0009](0009-domain-events-outbox-river.md); completes [ADR-0017](0017-business-review-and-admin-access.md)'s outbox move

## Context
M3.6 adds the first event that crosses modules: when an admin approves a business, `billing`
starts a free trial. ADR-0009 chose a transactional outbox on River. This slice decides how the
outbox is built, where plans and limits live, and how `business` checks them without the two
modules depending on each other in a circle.

## Decision
- **One job per subscriber.**
  - `outbox.Bus.PublishTx` inserts one River job per subscriber of each event, in the caller's
    transaction. When a handler fails, River retries that handler alone (with backoff); the
    other subscribers aren't run again.
  - Events nobody subscribes to insert nothing. An event goes to the subscribers known when it
    is published: a subscriber added later doesn't receive old events.
  - Subscriber names are stored in the jobs, so a name is never renamed.
  - Handlers must be idempotent (delivery is at least once). Billing's trial is an
    `INSERT … ON CONFLICT DO NOTHING`, and it counts from the approval time carried in the
    event, not from when the handler runs.
- **The approval and its event commit together.**
  - `Business.Approve` records a `BusinessApproved` event. The repository publishes the
    aggregate's events inside the same transaction as the update.
  - A test compares Postgres's `xmin` (the ID of the transaction that wrote a row) on the
    business row and on the job.
- **Event contracts are a leaf package** (`business/events`): plain JSON structs, owned by the
  publisher. Changing a field breaks every subscriber, so fields are only added.
  - The `business` root package offers a typed `OnApproved(bus, name, fn)`.
  - `main` connects it to `billing.StartTrial`. So `billing` never imports `business`, and
    `business` can still ask `billing` for limits without an import cycle. Lint rules enforce
    this.
- **River runs in its own schema** (`river`) with its own versioned migrations.
  - `database.Migrate` runs them after goose, under a session advisory lock.
  - River applies some migrations in separate transactions: a new enum value can't be used in
    the transaction that adds it.
  - Upgrading River never needs a hand-copied SQL file.
- **A `worker` role.** `server worker` builds the same modules as `server api` and runs the
  River client until SIGTERM. Running jobs get `WORKER_SHUTDOWN_TIMEOUT`, then they are
  cancelled and retried later. The api role only inserts jobs.
- **Plans are reference data in code:**
  - `free`: 1 branch, 3 staff.
  - `pro`: 5 branches, 30 staff.
  - "Staff" means managers and barbers plus open invitations. The owner doesn't count.
  - Before approval a business is in `setup` and gets the Pro limits: nothing is public yet, so
    nothing is being sold.
  - Approval starts a 30-day Pro trial. After it the business is on Free.
  - The trial's end is **computed on read**, not by a scheduled job, so there is no job to miss.
- **Limits are checked when adding, under the business lock.**
  - Adding a branch or inviting someone takes the `FOR NO KEY UPDATE` lock on the business row,
    counts, then inserts. Two requests at once can't both take the last place.
  - Over the limit: `409 plan_limit_reached`.
  - Nothing is removed when a plan shrinks. Existing branches and staff stay, and invitations
    already sent can still be accepted.
- **The owner sees the plan through `business`** (`GET /v1/businesses/{id}/subscription`).
  Only `business` knows who may see it; `billing` has no HTTP API.

## Consequences
- When a trial ends, a business keeps what it has but can't add more. Unpublishing branches
  beyond the Free limit, as `docs/architecture/domain-model.md` §3.7 describes, arrives with
  branch publishing. It will need a `SubscriptionExpired` event, which will be a scheduled River
  job.
- Payments, paid plans and admin-assigned plans come later. Only `trialing` is stored so far.
- Deployments run two processes from one image (`api`, `worker`). If the worker is down,
  approvals still succeed and trials start as soon as it is back.
