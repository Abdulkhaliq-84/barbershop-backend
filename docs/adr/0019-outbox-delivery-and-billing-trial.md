# ADR-0019: Outbox delivery per subscriber, a worker role, and billing limits

- Status: Accepted · Date: 2026-09-30 · Implements [ADR-0009](0009-domain-events-outbox-river.md); completes [ADR-0017](0017-business-review-and-admin-access.md)'s outbox move

## Context
M3.6 adds the first event that crosses modules: when an admin approves a business, `billing`
starts a free trial. ADR-0009 chose a transactional outbox on River. This slice decides how the
outbox is built, where plans and limits live, and how `business` checks them without the two
modules depending on each other in a circle.

## Decision
- **One delivery job per subscriber.**
  - When a handler fails, River retries that handler alone (with backoff); the other
    subscribers aren't run again.
  - *Changed after review (M4):* `outbox.Bus.PublishTx` inserts one `outbox_event` job per
    event, in the caller's transaction. The worker fans it out: one `outbox_delivery` job per
    subscriber, inserted in the same transaction that marks the event done
    (`river.JobCompleteTx`).
  - The fan-out uses the subscribers the publishing release knew (carried in the job) plus the
    ones the worker's release knows. This matters while a release rolls out and the api and
    worker run different versions:
    - A new subscriber that only the api knows waits for a worker that has it. A worker that
      doesn't know a subscriber snoozes the delivery (`river.JobSnooze`, which uses no attempts)
      every minute for up to a day, then drops it with a warning (a removed subscriber).
    - A new subscriber that only the worker knows gets events from older apis too.
    - Before, fan-out happened at publish time and an unknown subscriber cancelled its job, so
      either order of upgrading could silently lose an event.
  - A subscriber added later doesn't receive events that were fanned out before it existed.
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
  River client until SIGTERM. Running jobs get `WORKER_SHUTDOWN_TIMEOUT` (River's
  `SoftStopTimeout`), then they are cancelled and retried later. The api role only inserts jobs,
  through a client that has no queues. If River stops by itself, the worker exits with an error
  so the platform restarts it.
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
- A delivery that uses up River's 25 attempts (about three weeks of backoff) is discarded. A
  reconciliation job (for example "approved, but no subscription") is still to come; until
  then, discarded jobs stay visible in `river.river_job` for an operator.
